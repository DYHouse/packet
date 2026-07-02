# 结算模块代码审查报告

> 审查范围：`backend/settlement/service/` 全部 16 个 Go 文件 + `backend/game/infrastructure/messaging/game_event_consumer.go`
> 审查重点：(1) 实现风格一致性；(2) 幂等性真伪分析
> 审查日期：2026-07-01

---

## 目录

1. [总体结论](#1-总体结论)
2. [实现风格不一致问题](#2-实现风格不一致问题)
3. [幂等性深度分析](#3-幂等性深度分析)
4. [假幂等风险清单](#4-假幂等风险清单)
5. [修复建议](#5-修复建议)

---

## 1. 总体结论

| 维度 | 评级 | 说明 |
|------|------|------|
| **风格一致性** | ⚠️ 中等 | 错误处理、事务边界、锁实现、重试模式存在系统性分裂 |
| **幂等性** | ❌ 严重 | 存在 **19 处** 假幂等，其中 **8 处高风险**，资金安全依赖外部条件而非代码保证 |

**核心发现**：

1. **BizOrderNo 生成方式是根本缺陷** — 使用 `时间戳 + 随机数` 而非业务语义确定性 ID，重试时 BizID 不可复现，平台无法基于 BizID 做幂等去重。
2. **settlePlayer 完全无幂等保护** — 不创建 bill_record、不检查 GameSettleStatus、每次生成新 BizOrderNo，重试必重复结算。
3. **SELECT-then-UPDATE 普遍存在且无乐观锁** — 几乎所有状态机更新都先 SELECT 检查、再无条件 UPDATE，仅靠 Redis 分布式锁兜底。
4. **跨事务一致性漏洞** — `handleRoundSettle` 的 DB 事务与 `SettleRound` 内部独立事务不共享连接，前者失败后后者无法回滚。

---

## 2. 实现风格不一致问题

### 2.1 错误处理 — 三层混乱

| 风格 | 代表文件 | 示例 |
|------|----------|------|
| **service 层 `fmt.Errorf %w` 包装** | [balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/balance_service.go) L46 | `return fmt.Errorf("...: %w", err)` |
| **repository 层裸返回** | [bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) L48-51 | `return nil, err` |
| **`_` 丢弃 err（大量）** | [deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L67 | `if exists, _ := s.billMgr.ExistsRoundSettlement(...)` |

**同文件内自相矛盾**：

[settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go):
- L410-411: `return fmt.Errorf("parse balance amount failed: %w", err)` — 包装
- L435: `return platform.ParseAmount(result.Data.Balance.Amount)` — 裸返回

**错误吞没清单**（返回值被丢弃）：

| 文件 | 行号 | 被丢弃的调用 |
|------|------|-------------|
| deduct_service.go | L67, L76, L411, L417, L464, L469 | `ExistsRoundSettlement` / `ExistsByRoundAndType` 的 err |
| deduct_service.go | L204, L214, L239, L316 | `UpdateBillStatus` 返回值 |
| deduct_service.go | L207, L215, L240, L255 | `GetBalance` / `CreateDebitFailedException` |
| deduct_service.go | L517, L522, L523 | `UpdateRoundSettlementStatus` 等 |
| credit_retry_service.go | L121, L145 | `UpdateBillStatus` 返回值 |
| platform_call_manager.go | L27, L61 | `json.Marshal` err |
| refund_service.go | L175 | `CreateLog` 返回值 |
| settlement_service.go | L255 | `GetBalance` err |
| virtual_balance_service.go | L49 | `SAdd` 返回值 |
| virtual_balance_service.go | L66 | `fmt.Sscanf` 返回值 |

### 2.2 事务边界 — refund_service 越层

[refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) L23 持有 `db *gorm.DB` 字段，L105-120 在 service 层直接开事务：

```go
s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    // ...
})
```

而其他所有 service 都委托给 `billMgr` 封装的事务方法（[bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) L146/L157/L171/L243/L402）。这是全目录唯一例外，破坏了 repository 对事务的封装。

### 2.3 锁实现 — DCL 使用不统一

| 服务 | DCL | 锁 TTL | 说明 |
|------|-----|--------|------|
| DeductService（3 处） | ✅ 有 | 60/30 混用 | [deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L67+L76, L411+L417, L464+L469 |
| SettlementService.SettleRound | ⚠️ 半 DCL | 30 | 锁外吞 err、锁内抛 err，行为相反 |
| RefundService.ApproveRefund | ✅ 有 | 30 | 锁内复检返回 nil（静默） |
| RefundService.RejectRefund | ✅ 有 | 30 | 锁内复检返回 error |
| RefundService.ApplyForRefund | ❌ 无 | 30 | 无预检查 |
| GameSettleService.SettleGame | ❌ 无 | 60 | 无预检查 |
| GameSettleService.RetryPlayerSettle | ❌ 无 | 30 | 无预检查 |
| CreditRetryService.RetryCredit | ❌ 无 | 30 | 仅锁内单次检查 |

**同文件内自相矛盾**：[refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) — ApproveRefund L143 锁内复检静默返回 nil，RejectRefund L225 锁内复检返回 error。

**锁 TTL 全为魔法数字**：30/60 秒散落于 7 个文件 9 处调用，无一常量化。

### 2.4 重试模式 — 分裂为两套

| 服务 | 重试模式 | 参数 |
|------|---------|------|
| [credit_retry_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go) L29-31 | 指数退避 + 配置 | BaseDelay=5s, MaxDelay=5min, Multiplier=2.0, MaxRetry=3 |
| [game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) L365 | 固定延迟 + 魔法数字 | `5 * time.Second` 硬编码 |

两套重试互不相通，参数不可共享。

### 2.5 Repository 命名不统一

| Repository | 命名风格 | 示例 |
|------------|---------|------|
| BillManager | 实体前缀式 | `GetBillByID`, `CreateBill`, `UpdateBillStatus` |
| ExceptionManager | 泛型式 | `GetByID`, `Create`, `UpdateStatus` |
| PlatformCallManager | Log 前缀 | `CreateLog`, `GetLogByID` |
| BillManager.ExistsByRoundAndType vs ExistsRoundSettlement | 一个带 By 一个不带 | 不一致 |

`First` 用法：[exception_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/exception_manager.go) L25 用位置参数 `First(&exception, id)`，其他一律 `.Where("id = ?", id).First()` 链式。

### 2.6 配置注入不统一

| 服务 | 字段名 | nil 兜底 | 可注入 |
|------|--------|---------|--------|
| BalanceService | `cfg` | ✅ | ✅ |
| DeductService | `cfg` | ✅ | ✅ |
| RewardSettler | `config` | ❌ 无 | ✅ |
| CreditRetryService | `retryCfg` | — | ❌ 构造函数内硬编码 `DefaultCreditRetryConfig()` |

[reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go) L32-39 无 nil 兜底，传入 nil 会在 L42 `s.config.Enabled` 空指针 panic。

### 2.7 ctx 传递

[trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) 所有 `Generate*` 方法（L20/L24/L30/L34/L38/L44/L50/L54）均不接受 `ctx context.Context`，是全目录唯一例外。

### 2.8 魔法数字清单

| 类型 | 出现位置 | 值 |
|------|---------|-----|
| 锁 TTL | deduct_service.go L74/L416/L468, credit_retry_service.go L79, game_settle_service.go L61/L239, refund_service.go L57/L136/L218, settlement_service.go L74 | 30 或 60 |
| 重试延迟 | game_settle_service.go L365 | `5 * time.Second` |
| rewardType | reward_settler.go L48/L51 | `case 1` / `case 2` |
| limit | settlement_check_service.go L35/L54 | `100` |
| 时间阈值 | settlement_check_service.go L35 | `10 * time.Minute` |
| 随机范围 | trace_id_generator.go L27/L40/L46 | `1000` / `10000` |

对比规范做法：[deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L19 `const defaultMaxConcurrentDeduct = 20` ✅

### 2.9 virtual_balance_service 原子性不一致

[virtual_balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/virtual_balance_service.go):
- `Deduct` (L28-39)：用 Lua 脚本保证原子性 ✅
- `Credit` (L42-51)：用 `IncrBy` + `SAdd` 两步，**非原子** ❌ — SAdd 失败不会回滚 IncrBy

---

## 3. 幂等性深度分析

### 3.1 幂等键的唯一性

#### BizOrderNo — 假幂等（根本缺陷）

文件：[trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) L24-28

```go
func (g *TraceIDGenerator) GenerateBizOrderNo(bizType string, userID int64) string {
    timestamp := time.Now().Format("20060102150405") // 精确到秒
    random := g.idGen.GenerateInt64() % 1000          // 仅 0-999
    return fmt.Sprintf("%s_%s_%d_%03d", bizType, timestamp, userID, random)
}
```

**问题**：
1. **重试不可复现**：每次调用用 `time.Now()` + 新雪花 ID 取模，同一逻辑操作重试时生成**不同**的 BizOrderNo。平台无法基于 BizID 做幂等去重。
2. **碰撞风险**：同一秒 + 同一 bizType + 同一 userID 下，random 仅有 1000 种可能。高并发下碰撞非零，虽有 `uniqueIndex` 兜底但会导致 INSERT 失败、整个操作报错。
3. **关键场景**：`settlePlayer`（[game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) L183）每次调用都生成新 BizOrderNo。

**正确设计对比**：`GenerateRoundTraceID` (L20-22) 基于 `sessionID + roundNo` 确定性生成，可复现，是真正的幂等键。

### 3.2 幂等检查的原子性

#### SettleRound — SELECT-then-UPDATE 无乐观锁

文件：[settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go) L67-124, [bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) L89-99

```go
// L68-71: 锁前检查（err 被忽略）
settlement, err := s.billMgr.GetRoundSettlementByRoundID(ctx, req.RoundID)
if err == nil && settlement != nil && settlement.Status == dto.RoundStatusCredited {
    return nil
}
// L74: Redis 锁
lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
    // L75-85: 锁内二次检查
    if existingSettlement.Status == dto.RoundStatusCredited { return nil }
    // ... 执行操作 ...
    // L118: 无条件 UPDATE
    s.billMgr.UpdateRoundSettlementCredited(ctx, ...)
})
```

`UpdateRoundSettlementCredited` 实现：
```go
Where("round_trace_id = ?", traceID).Updates(map[...]{
    "status": dto.RoundStatusCredited, ...
})
```

**问题**：UPDATE 没有 `WHERE status != Credited` 的乐观锁条件，无法防止并发覆盖。仅靠 Redis 锁兜底，Redis 主从切换或 watchdog 续期失败时即崩溃。

#### creditSessionPayout — 查询与创建无事务无锁

文件：[game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) L288-323

```go
existingBills, err := s.billMgr.GetBillsBySessionTypeAndUser(...)  // 查询
for _, bill := range existingBills {
    if bill.Status == dto.BillStatusSuccess { return nil }
}
bill := &model.BillRecord{...}
s.billMgr.CreateBill(ctx, bill)  // 创建（独立事务，无 SELECT FOR UPDATE）
```

**问题**：查询与创建非原子（TOCTOU 漏洞），虽有 `GameSettleLockKey(sessionID)` 串行化，但锁失效时不安全。

#### SettleReward — 检查与创建不在同一事务

文件：[reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go) L69-111

```go
existingBill, err := s.billMgr.GetBillByRoundTypeAndUser(...)  // 检查
if err == nil && existingBill != nil && existingBill.Status == dto.BillStatusSuccess {
    return nil
}
// ... 构造 bills ...
return s.billMgr.CreateBillsInTransaction(ctx, allBills)  // 创建（独立事务）
```

**问题**：检查和创建是两个独立 DB 操作，中间无锁无事务。且**查询 err 被丢弃**（L69 用 `err == nil` 判断而非显式处理），查询失败（非 NotFound）时会继续走创建分支。

### 3.3 状态机幂等的正确性

#### UpdateRoundSettlementCredited 无 WHERE status 条件

[bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) L89-99：

```go
Where("round_trace_id = ?", traceID).Updates(map[...]{
    "status": dto.RoundStatusCredited, ...
})
```

**应改为**：`Where("round_trace_id = ? AND status != ?", traceID, dto.RoundStatusCredited)`，并检查 `RowsAffected`。

同理 `UpdateRoundSettlementStatus` (L101-111)、`UpdateRefundAuditStatus` (L225-235) 均无状态条件。

### 3.4 重试场景下的幂等

#### settlePlayer — 最严重的假幂等

文件：[game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) L164-234

```go
func (s *GameSettleService) settlePlayer(...) error {
    // L166: 机器人跳过
    // L183: 每次生成新 BizOrderNo
    bizOrderNo := s.traceIDGen.GenerateBizOrderNo("GAME_SETTLE", userID)
    // L208: 调用平台
    result, err := s.platform.Settle(ctx, settleReq)
    // L221: 更新状态（失败仅日志）
    s.billMgr.UpdateGameSettleStatusByUser(ctx, sessionID, userID, dto.BillGameSettleSettled)
}
```

**三个关键缺陷**：
1. **不创建 bill_record** — 无本地幂等凭据
2. **不检查 GameSettleStatus** — 不查玩家是否已结算
3. **每次生成新 BizOrderNo** — 平台无法识别为重复

**失效场景**：`platform.Settle` 成功但 `UpdateGameSettleStatusByUser` 失败 → `SettleGame` 重试时再次调用 `settlePlayer` → 用新 BizOrderNo 再次 `platform.Settle` → **重复结算**。

#### SettleGame 重试不跳过已成功玩家

文件：[game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) L127-145

```go
// L72-81: 只检查整体是否 allSettled
allSettled := true
for _, rs := range settlements {
    if rs.GameSettleStatus != dto.GameSettleStatusSuccess { allSettled = false; break }
}
if allSettled { return nil }

// L127-145: 遍历所有玩家，无单个玩家状态过滤
for userID := range allUsers {
    // L132-134: 仅跳过 platform account
    // L137-139: 仅跳过无活动玩家
    // 没有 "已 Settled 的玩家跳过" 的检查！
    s.settlePlayer(ctx, sessionID, userID, ...)
}
```

**失效场景**：部分玩家已 Settled、部分未 Settled → `allSettled = false` → 重试时对**所有玩家**重新调用 `settlePlayer` → 已 Settled 的玩家被重复结算。

#### executeSingleDeduct — 扣款成功但状态更新失败

文件：[deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L237-270

```go
result, err := s.platform.Debit(ctx, debitReq)  // L237 平台扣款
if err != nil { ... return err }
// L267: 更新 bill 为 Success
if err := s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter); err != nil {
    return err  // bill 仍为 Processing！
}
```

**失效场景**：`platform.Debit` 成功（用户已扣款）但 `UpdateBillSuccess` 失败（DB 闪断）→ bill 仍为 Processing → **无重试逻辑**，资金卡死 → 且 `CreateDebitFailedException` 会错误地创建"扣款失败"异常（实际已扣款）。

#### 平台 API 超时 — 无超时查询机制

文件：[deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L237, [credit_retry_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go) L143

**失效场景**：`platform.Debit`/`Credit` 超时，平台侧实际已扣款/入账，但本地收到 timeout → 标记 bill Failed → 重试时盲目再次调用 → **完全依赖平台幂等能力**（代码中无任何证据表明平台保证幂等）。

### 3.5 跨表/跨服务的幂等

#### handleRoundSettle 事务与 SettleRound 跨事务 — 严重一致性漏洞

文件：[game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) L234-367

```go
if err := c.db.Transaction(func(tx *gorm.DB) error {
    // L235-243: 更新 round（用 tx）
    // L245-272: 创建 grab_record（用 tx，FirstOrCreate）
    // L306-326: 创建 special_reward（用 tx，直接 Create）
    // L358: 调用 SettleRound（用 billMgr 独立连接，非 tx！）
    if err := c.settlementService.SettleRound(ctx, settleReq); err != nil {
        return fmt.Errorf("settle round failed: %w", err)
    }
    return nil
}); err != nil { return err }
```

**失效场景**：`SettleRound` 成功（round_settlement 已 Credited，bill_record 已提交）但外层 `tx` 提交失败 → round 未更新、grab_record 未创建、special_reward 未创建，但 round_settlement 已 Credited。

**重试时**：`handleRoundSettle` 再次执行 → round 更新、grab_record FirstOrCreate（创建）、**special_reward 直接 Create（重复创建！）**、`SettleRound` 检查 Credited 直接返回 nil（不再补偿）。

#### bill_record 与 platform_call_log 跨事务

文件：[deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L231-278

```go
callLog, _ := s.callMgr.CreateLog(ctx, ...)  // L231 创建 call_log（独立事务，err 被丢弃！）
result, err := s.platform.Debit(ctx, debitReq)  // L237 调用平台
s.billMgr.UpdateBillSuccess(ctx, bill.ID, ...)  // L267 更新 bill
s.callMgr.UpdateLog(ctx, ...)  // L272 更新 call_log
```

**问题**：
1. `CreateLog` 失败被 `_` 忽略，`callLog` 为 nil，但 `platform.Debit` 仍执行
2. 重试时**不查询 call_log** 来判断平台是否已调用，直接再次调用
3. bill_record 和 platform_call_log 无法原子提交

### 3.6 tryAcquire — SetNX fail-open + TTL 风险

文件：[game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) L449-462

```go
func (c *GameEventConsumer) tryAcquire(ctx context.Context, traceID string) bool {
    if c.redis == nil { return true }
    key := redisKeys.GameEventProcessedKey(traceID)
    ok, err := c.redis.SetNX(ctx, key, 1, 7*24*time.Hour).Result()
    if err != nil {
        logger.Warn("tryAcquire SetNX failed, fail-open", ...)
        return true  // fail-open！
    }
    return ok
}
```

**两个风险**：
1. **fail-open**：Redis 出错时直接返回 true，资金类操作极其危险
2. **TTL 过期**：7 天后 key 自动过期，若 Kafka 堆积超过 7 天后重投递，`tryAcquire` 再次通过

### 3.7 惩罚扣款/分配 — 完全无幂等检查

文件：[settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go)

- `DeductPenaltyToPlatform` (L213-315)：不检查是否已存在同 RoundTraceID 的 bill，直接创建 bills + 调用平台
- `DistributePenaltyFromPlatform` (L317-364)：同上

RoundTraceID 虽然是确定性的（`PENALTY_DED_<roomID>_<sessionID>`），但代码中**不基于它查重**。调用两次会创建两对 bills + 两次平台扣款/入账。

---

## 4. 假幂等风险清单

### 4.1 高风险（8 处）

| # | 检查点 | 文件:行号 | 失效场景 |
|---|--------|-----------|----------|
| H1 | BizOrderNo 含时间戳+随机数，重试不可复现 | trace_id_generator.go:24-28 | 重试时生成新 BizOrderNo，平台无法基于 BizID 去重 |
| H2 | settlePlayer 无 bill_record + 无幂等检查 + 新 BizOrderNo | game_settle_service.go:164-234 | Settle 成功 + UpdateStatus 失败 → 重试重复结算 |
| H3 | SettleGame 重试不跳过已成功玩家 | game_settle_service.go:127-145 | 部分玩家已 Settled，重试时对所有玩家重复 Settle |
| H4 | executeSingleDeduct 扣款成功但状态更新失败 | deduct_service.go:237-270 | Debit 成功 + UpdateBillSuccess 失败 → 资金卡死 + 错误异常 |
| H5 | 平台 API 超时无查询机制 | deduct_service.go:237, credit_retry_service.go:143 | 超时但平台已扣款，重试重复扣款 |
| H6 | handleRoundSettle 事务与 SettleRound 跨事务 | game_event_consumer.go:234-367 | SettleRound 成功但 tx 失败 → special_reward 重复创建 |
| H7 | tryAcquire SetNX fail-open | game_event_consumer.go:449-462 | Redis 出错时 fail-open 导致并发重复处理 |
| H8 | 惩罚扣款/分配完全无幂等检查 | settlement_service.go:213-364 | 调用两次创建两对 bills + 两次平台调用 |

### 4.2 中风险（11 处）

| # | 检查点 | 文件:行号 | 失效场景 |
|---|--------|-----------|----------|
| M1 | SESSION_CREDIT 的 BizOrderNo 随机生成 | game_settle_service.go:303-306 | 并发创建时生成不同 BizOrderNo |
| M2 | SettleRound SELECT-then-UPDATE 无乐观锁 | settlement_service.go:67-124 | Redis 锁失效时并发覆盖状态 |
| M3 | creditSessionPayout 查询与创建非原子 | game_settle_service.go:288-323 | 并发创建多条 bill |
| M4 | SettleReward 检查与创建不在同一事务 | reward_settler.go:69-111 | Redis 锁失效时重复发放奖励 |
| M5 | FirstOrCreate 无唯一索引时不原子 | game_event_consumer.go:129,260 | 无唯一索引时重复插入 |
| M6 | UpdateRoundSettlementCredited 无 WHERE status | bill_manager.go:89-99 | 并发覆盖 Credited 状态 |
| M7 | doRetryCredit SELECT-then-act 非原子 | credit_retry_service.go:84-107 | Redis 锁失效时重复 Credit |
| M8 | ApproveRefund 锁内检查+更新非原子 | refund_service.go:125-154 | 锁失效时并发退款 |
| M9 | bill_record 与 platform_call_log 跨事务 | deduct_service.go:231-278 | CreateLog 失败被忽略，无法判断平台是否已调用 |
| M10 | executeRefund 平台调用与状态更新跨事务 | refund_service.go:156-205 | Credit 成功 + 事务失败 → 重试重复退款 |
| M11 | reward_settler 查询 err 被丢弃 | reward_settler.go:69-72 | 查询失败时继续走创建分支，可能重复 bill |

### 4.3 低风险（3 处）

| # | 检查点 | 文件:行号 | 说明 |
|---|--------|-----------|------|
| L1 | RoundTraceID + BillType + UserID 无 DB 唯一索引 | bill_record 表 | 逻辑唯一但缺强约束 |
| L2 | RetryCount 达上限后不再重试 | credit_retry_service.go:94-96 | 无循环风险，正确 |
| L3 | releaseAcquire 时机正确 | game_event_consumer.go:77 | 失败才释放，成功保留 7 天 |

---

## 5. 修复建议

### 5.1 P0 — 立即修复（资金安全风险）

#### 5.1.1 BizOrderNo 改为确定性生成

```go
// 修改前（trace_id_generator.go L24-28）
func (g *TraceIDGenerator) GenerateBizOrderNo(bizType string, userID int64) string {
    timestamp := time.Now().Format("20060102150405")
    random := g.idGen.GenerateInt64() % 1000
    return fmt.Sprintf("%s_%s_%d_%03d", bizType, timestamp, userID, random)
}

// 修改后：基于业务语义确定性生成
func (g *TraceIDGenerator) GenerateBizOrderNo(roundTraceID string, billType int, userID int64) string {
    return fmt.Sprintf("%s_%d_%d", roundTraceID, billType, userID)
}
```

影响范围：所有调用 `GenerateBizOrderNo` 的地方需传入 `roundTraceID` 和 `billType`。

#### 5.1.2 settlePlayer 增加幂等保护

```go
func (s *GameSettleService) settlePlayer(ctx context.Context, sessionID int64, userID int64, ...) error {
    // 1. 幂等检查：查询是否已结算
    settled, err := s.billMgr.IsPlayerGameSettled(ctx, sessionID, userID)
    if err != nil {
        return fmt.Errorf("check player settled failed: %w", err)
    }
    if settled {
        return nil  // 已结算，跳过
    }
    // 2. 确定性 BizOrderNo
    bizOrderNo := fmt.Sprintf("GAME_SETTLE_%d_%d", sessionID, userID)
    // 3. 调用平台
    result, err := s.platform.Settle(ctx, settleReq)
    // 4. 更新状态
}
```

#### 5.1.3 SettleGame 重试跳过已成功玩家

```go
for userID := range allUsers {
    // 检查单个玩家是否已 Settled
    settled, _ := s.billMgr.IsPlayerGameSettled(ctx, sessionID, userID)
    if settled {
        continue  // 跳过已结算玩家
    }
    s.settlePlayer(ctx, sessionID, userID, ...)
}
```

#### 5.1.4 状态机 UPDATE 增加乐观锁

```go
// bill_manager.go - UpdateRoundSettlementCredited
result := tx.Model(&model.RoundSettlement{}).
    Where("round_trace_id = ? AND status != ?", traceID, dto.RoundStatusCredited).
    Updates(map[...]{
        "status": dto.RoundStatusCredited, ...
    })
if result.RowsAffected == 0 {
    return fmt.Errorf("round already credited or not found: %s", traceID)
}
```

#### 5.1.5 tryAcquire 改为 fail-closed

```go
func (c *GameEventConsumer) tryAcquire(ctx context.Context, traceID string) bool {
    if c.redis == nil { return true }
    key := redisKeys.GameEventProcessedKey(traceID)
    ok, err := c.redis.SetNX(ctx, key, 1, 7*24*time.Hour).Result()
    if err != nil {
        logger.Error("tryAcquire SetNX failed, fail-closed", ...)
        return false  // fail-closed，宁可漏处理也不要重复处理
    }
    return ok
}
```

#### 5.1.6 惩罚扣款/分配增加幂等检查

```go
func (s *SettlementService) DeductPenaltyToPlatform(ctx context.Context, req *dto.PenaltyDeductRequest) error {
    roundTraceID := s.traceIDGen.GeneratePenaltyDeductTraceID(req.RoomID, req.SessionID)
    // 幂等检查
    existing, err := s.billMgr.GetBillByRoundTypeAndUser(ctx, 0, dto.BillTypePenaltyIncome, req.UserID)
    if err == nil && existing != nil && existing.RoundTraceID == roundTraceID {
        return nil  // 已存在，跳过
    }
    // ... 继续创建 ...
}
```

### 5.2 P1 — 尽快修复（一致性风险）

| 问题 | 修复方案 |
|------|---------|
| handleRoundSettle 与 SettleRound 跨事务 | 将 SettleRound 改为接受 `*gorm.DB` 参数，在外层事务内执行 |
| bill_record 与 platform_call_log 跨事务 | call_log 创建失败应中止平台调用 |
| 平台超时无查询机制 | 增加 `platform.QueryOrder(BizID)` 接口，超时后先查询再决定是否重试 |
| creditSessionPayout TOCTOU | 创建 bill 时用 `INSERT ... ON DUPLICATE KEY` 或加唯一索引 |
| SettleReward 检查与创建非原子 | 将检查放入同一事务内，用 `SELECT ... FOR UPDATE` |

### 5.3 P2 — 风格统一（可维护性）

| 问题 | 修复方案 |
|------|---------|
| 错误处理三层混乱 | repository 层统一包装 `fmt.Errorf`；禁止 `_` 丢弃 err |
| refund_service 事务越层 | 移除 `db` 字段，事务逻辑下沉到 billMgr |
| 锁 TTL 魔法数字 | 提取常量 `const DefaultLockTTL = 30 * time.Second` |
| 重试模式分裂 | 统一用 `CreditRetryConfig`，移除 `game_settle_service.go` L365 的固定 5s |
| Repository 命名不统一 | 统一为 `Get<Entity>ByID` / `Create<Entity>` / `Update<Entity>Status` 风格 |
| RewardSettler 无 nil 兜底 | 构造函数增加 `if rewardCfg == nil { rewardCfg = DefaultRewardSettlementConfig() }` |
| virtual_balance Credit 非原子 | 用 Lua 脚本合并 IncrBy + SAdd |
| ctx 传递 | `Generate*` 方法增加 ctx 参数 |

---

## 附录：审查文件清单

| 文件 | 已审查 |
|------|--------|
| [settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go) | ✅ |
| [game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) | ✅ |
| [deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) | ✅ |
| [bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) | ✅ |
| [reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go) | ✅ |
| [balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/balance_service.go) | ✅ |
| [credit_retry_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go) | ✅ |
| [refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) | ✅ |
| [exception_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/exception_manager.go) | ✅ |
| [platform_call_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/platform_call_manager.go) | ✅ |
| [robot_checker.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/robot_checker.go) | ✅ |
| [settlement_check_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_check_service.go) | ✅ |
| [user_id_convert_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/user_id_convert_service.go) | ✅ |
| [virtual_balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/virtual_balance_service.go) | ✅ |
| [trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) | ✅ |
| [lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go) | ✅ |
| [game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) | ✅ |
