# 重构方案：回合级扣款 + 回合级入账 + 游戏级结算

## 一、现状分析

### 1.1 当前结算流程

```
Round N 结束
  ├─ 1. DeductService: 调用 platform.Debit() 扣款
  ├─ 2. SettlementService.SettleRound: 调用 platform.Settle(/credit_n_settle) 入账+结算
  │    ├─ settleCommission: 佣金记账（无平台调用）
  │    └─ 对每个玩家调用 platform.Settle(bet_amount, payout, result)
  └─ 3. RewardSettler: 调用 platform.Settle(/credit_n_settle) 奖励入账+结算
```

**核心问题：`platform.Settle()` 做了两件事 — 入账（给玩家加钱）和结算（上报 bet_amount/payout/result），耦合在一起。**

### 1.2 platform.Settle 的语义

```
platform.Settle(端点: /credit_n_settle) 的原子操作：
  balance = balance - betAmount + payout

等价于：Debit(betAmount) + Credit(payout) + 记录游戏结果

平台另有 /settle 端点，参数相同，只记录游戏结果，不修改余额（不入账只结算）。
```

当前每个回合调用一次 Settle，bet_amount 的值拼凑且语义不清：

| 发送者类型 | 玩家角色 | bet_amount | payout | game_result |
|---|---|---|---|---|
| 玩家发红包 | 最小金额玩家 | roomFee（红包总额） | 抢到金额 | 通常 lose |
| 玩家发红包 | 非最小金额玩家 | "0" | 抢到金额 | 必然 win |
| system | 所有玩家 | roomFeePerPlayer | 抢到金额 | 看金额大小 |
| system_resume/system_forced | 所有玩家 | "0" | 抢到金额 | 必然 win |
| 罚金分发 | 接收者 | "0" | 分摊金额 | 必然 win |

**问题**：大部分场景 bet_amount="0"，"下注为0赢了钱"的记录监管不合规；isMin 玩家的 bet_amount=roomFee 是拼凑的语义，不是真实投入。

---

## 二、重构目标

将当前"入账+结算"合一的 `platform.Settle(/credit_n_settle)` 调用，拆分为：

```
Round N 结束:
  ├─ 1. 扣款: platform.Debit(/debit)（不变）
  └─ 2. 入账: platform.Credit(/credit)（拆出来，只加钱，不上报游戏结果）

Game 结束（10回合完成）:
  └─ 3. 结算: platform.Settle(/settle)（统一上报，只结算不入账，bet_amount=整局总投入，payout=整局总收入）
```

| | 扣款 | 入账 | 结算 |
|---|---|---|---|
| 当前 | 回合级 Debit(/debit) | 回合级（含在Settle中） | 回合级 Settle(/credit_n_settle) |
| 重构后 | 回合级 Debit(/debit)（不变） | 回合级 Credit(/credit)（拆出） | 游戏级 Settle(/settle)（延迟） |

---

## 三、关键设计问题：资金不重复移动

### 3.1 问题

当前 `platform.Settle()` 调用的是平台端点 `/credit_n_settle`，其语义是原子操作：

```
balance = balance - betAmount + payout
等价于：Debit(betAmount) + Credit(payout) + 记录游戏结果
```

如果每回合已经通过 Debit 扣了款、通过 Credit 加了钱，游戏结束时再调 `/credit_n_settle(betAmount, payout)` 会**重复记账**。

### 3.2 解决方案：使用平台 `/settle` 端点

**平台已有 `/settle` 端点，参数与 `/credit_n_settle` 完全一样，只是不入账只结算（只记录游戏结果，不修改余额）。**

这意味着：
- 回合级入账：调用 `platform.Credit()`（端点 `/credit`），只加钱
- 游戏级结算：调用 `platform.Settle()`（端点改为 `/settle`），只上报 bet_amount/payout/result，不移动资金

**不需要任何平台侧配合**，只需在客户端将 Settle 的端点从 `/credit_n_settle` 改为 `/settle`。

```
资金流（实时）:
  Round N: platform.Debit() 扣款 → platform.Credit() 入账
  → 余额实时变化，玩家随时可用

结算流（游戏结束时）:
  Game End: platform.Settle(/settle) 上报 bet_amount/payout/result
  → 仅记录游戏结果，不修改余额
```

### 3.3 platform.Client 接口变更

直接修改 `Settle()` 方法的端点，从 `/credit_n_settle` 改为 `/settle`：

```go
// 当前
type Client interface {
    Debit(...)   // POST /debit
    Credit(...)  // POST /credit
    Settle(...)  // POST /credit_n_settle（入账+结算）
    ...
}

// 重构后
type Client interface {
    Debit(...)   // POST /debit
    Credit(...)  // POST /credit
    Settle(...)  // POST /settle（只结算不入账）
    ...
}
```

所有原调用 `Settle()` 的地方统一调整：
- 回合级入账 → 改为调用 `Credit()`
- 游戏级结算 → 调用 `Settle()`（端点已改为 `/settle`，只结算不入账）
- 不保留旧逻辑，无向后兼容

---

## 四、数据模型变更

**不新增表**。游戏级结算所需的数据全部从已有 `BillRecord` + `RoundSettlement` 聚合得出，只对现有表增加少量字段。

### 4.1 RoundSettlement 增加字段

```go
// 新增字段
GameSettleStatus  int        // 游戏级结算状态: 0=未结算, 1=结算中, 2=成功, 3=失败
GameSettledAt     *time.Time // 游戏级结算完成时间
```

- 同一个 Game 的所有 RoundSettlement 共享相同的 `SessionID`，通过 `SessionID` 即可关联同一局游戏
- `GameSettleStatus` 只需要在**第一个回合**（RoundNo=1）上记录即可，代表整局游戏的结算状态
- 判断游戏是否可以结算：查询该 SessionID 下所有 RoundSettlement 是否都为 `Credited(6)` 状态

### 4.2 BillRecord 增加字段

```go
// 新增字段
GameSettleStatus  int        // 该玩家游戏级结算状态: 0=未结算, 1=已结算
```

- 游戏级结算时，按 `SessionID + UserID` 聚合所有 BillRecord
- 对每个玩家调用 `platform.Settle(/settle)` 成功后，将该玩家所有 Bill 的 `GameSettleStatus` 标记为已结算
- 未标记的 Bill 可被调度器扫描重试

### 4.3 数据聚合方式（运行时计算，不持久化）

```
游戏级结算时，按 SessionID 查询:

BetAmount (玩家总投入):
  SELECT user_id, SUM(ABS(amount)) FROM bill_record
  WHERE session_id = ? AND amount < 0 AND bill_type IN (2,4,8,9)
  GROUP BY user_id

PayOut (玩家总收入):
  SELECT user_id, SUM(amount) FROM bill_record
  WHERE session_id = ? AND amount > 0 AND bill_type IN (3,10,11)
  GROUP BY user_id

GameResult:
  "win" if PayOut > BetAmount else "lose"
```

### 4.4 RoundSettlement 状态流转变更

```
当前:  Deducting(0) → Deducted(1) → Settling(2) → Success(3)
重构后: Deducting(0) → Deducted(1) → Credited(6)    ← 新增，表示回合入账完成
```

- `Deducted(1)` → 扣款完成，准备入账
- `Credited(6)` → 入账完成（调用 `platform.Credit` 成功）
- 移除 `Settling(2)` 和 `Success(3)`，因为结算不再是回合级概念

### 4.5 dto/constants.go 新增枚举

```go
// RoundStatus 新增
RoundStatusCredited = 6  // 回合入账完成

// GameSettleStatus
GameSettleStatusNone     = 0  // 未结算
GameSettleStatusSettling = 1  // 结算中
GameSettleStatusSuccess  = 2  // 成功
GameSettleStatusFailed   = 3  // 失败

// BillRecord GameSettleStatus
BillGameSettleNone     = 0  // 未结算
BillGameSettleSettled  = 1  // 已结算
```

---

## 五、业务流程变更

### 5.1 回合结束流程（新）

```
Round N 结束
  │
  ├─ 1. 扣款（与现有逻辑完全相同）
  │    ├─ 首回合: DeductService.DeductForFirstRound → platform.Debit()
  │    ├─ 后续回合: DeductService.DeductForLaterRound → platform.Debit()
  │    ├─ 系统红包: DeductService.DeductForSystemPacket → platform.Debit()
  │    └─ 扣款Bill照常创建（BillType=2/4/9），SessionID 已有
  │
  ├─ 2. 更新RoundSettlement状态
  │    └─ Deducted → 进入入账阶段
  │
  ├─ 3. 入账（新逻辑，替代原SettleRound中的Settle调用）
  │    ├─ 对每个应入账的玩家:
  │    │    ├─ 创建入账Bill（BillType=3 GrabPacket, Amount=入账金额, 正数）
  │    │    ├─ 调用 platform.Credit()（只加钱，无游戏元数据）
  │    │    ├─ 成功: Bill.Status = Success
  │    │    └─ 失败: Bill.Status = Failed, 设置RetryCount/NextRetryAt, 进入Credit重试
  │    └─ 佣金: 仍为纯记账，BillType=7, 直接标记Success
  │
  ├─ 2. 更新RoundSettlement状态
  │    └─ Credited(6) — 回合入账完成
  │
  └─ 3. 检查是否触发游戏结算
       ├─ 查询该 SessionID 下所有 RoundSettlement 是否都为 Credited(6)
       └─ 若是 → 触发游戏结算（见5.2）
```

**与当前流程的对比**：

| 步骤 | 当前 | 重构后 |
|---|---|---|
| 扣款 | platform.Debit() | platform.Debit()（不变） |
| 入账 | platform.Settle(bet, payout, result) | platform.Credit(amount)（拆出） |
| 结算 | 包含在Settle中 | 延迟到游戏结束 |

### 5.2 游戏结算流程（新）

```
Game 结束（所有回合入账完成）
  │
  ├─ 1. 标记游戏结算状态
  │    └─ RoundSettlement(RoundNo=1).GameSettleStatus = Settling(1)
  │
  ├─ 2. 聚合每个玩家数据（从 BillRecord 运行时计算）
  │    ├─ 查该 SessionID 所有扣款Bill(amount<0) → 按 UserID 聚合 → BetAmount
  │    ├─ 查该 SessionID 所有入账Bill(amount>0) → 按 UserID 聚合 → PayOut
  │    └─ 对每个玩家: GameResult = "win" if PayOut > BetAmount else "lose"
  │
  ├─ 3. 逐玩家调用 platform.Settle(/settle)（只结算不入账）
  │    ├─ 构建 SettleRequest:
  │    │    ├─ BetAmount = 该玩家总扣款（整局真实总投入）
  │    │    ├─ PayOut = 该玩家总入账（整局真实总收入）
  │    │    ├─ Result = GameResult
  │    │    ├─ StartTime = 第一个 RoundSettlement.CreatedAt
  │    │    └─ EndTime = 最后一个 RoundSettlement.UpdatedAt
  │    ├─ 成功: 标记该玩家所有 Bill 的 GameSettleStatus = Settled(1)
  │    └─ 失败: 标记该玩家 Bill 的 GameSettleStatus 保持 None(0)，由调度器重试
  │
  ├─ 4. 奖励结算（逻辑不变，但时机调整）
  │    └─ 也纳入游戏级结算
  │
  └─ 5. 更新游戏结算最终状态
       ├─ 全部成功 → RoundSettlement(RoundNo=1).GameSettleStatus = Success(2)
       └─ 部分失败 → GameSettleStatus = Failed(3), 失败玩家的 Bill 由调度器重试
```

### 5.3 新 bet_amount 对比

**玩家发红包场景，假设 10 回合**：

| 角色 | 当前 bet_amount（每回合） | 重构后 bet_amount（游戏级） |
|---|---|---|
| 发红包者（每轮被扣 roomFee） | 每回合: isMin时=roomFee, 否则=0 | 整局: 10回合总扣款 |
| 其他玩家（首回合扣 roomFeePerPlayer） | 每回合: "0" | 整局: 首回合均摊 + 可能的罚金 |
| 最小金额玩家 | 每回合: roomFee（单个红包总额） | 整局: 所有回合扣款总和 |

**语义提升**：
- bet_amount = 玩家在这个游戏中实际付出的总金额（可从 Debit bill 直接汇总）
- payout = 玩家在这个游戏中实际收到的总金额（可从 Credit bill 直接汇总）
- game_result = 基于净收益判断 win/lose，不再有"下注0赢了钱"的荒谬记录

---

## 六、特殊情况处理

### 6.1 入账失败的重试

```
当前: CreditRetryScheduler 重试失败的 BillRecord（通过 platform.Settle 重试）
重构后: CreditRetryScheduler 重试失败的入账Bill（通过 platform.Credit 重试）

重试逻辑基本不变，只是将 platform.Settle → platform.Credit：
  - 入账Bill.Status = Failed 且 NextRetryAt <= now
  - 调用 platform.Credit() 重试
  - 成功: Status = Success
  - 失败: RetryCount++, NextRetryAt 指数退避
```

**关键区别**：入账重试不影响游戏级结算。即使某回合入账还在重试，游戏结算仍可触发（基于已有成功/失败的Bill聚合）。游戏结算上报失败由 GameSettleRetryScheduler 处理（重试调用 `platform.Settle(/settle)`）。

### 6.2 游戏异常中断

```
触发条件:
  - 房间关闭/会话结束
  - 管理后台手动触发
  - 调度器检测超时

处理:
  ├─ 标记未完成的 RoundSettlement 不再继续
  ├─ 已完成的回合(Credited): 正常参与游戏结算
  ├─ 未扣款的回合: 不参与结算
  └─ 已扣款但未入账的回合: 等入账完成后再触发游戏结算
       └─ 超时未入账 → ExceptionHandleScheduler 处理 → 退款
```

### 6.3 罚金操作

```
DeductPenaltyToPlatform（扣罚金）:
  - 不变，仍调用 platform.Debit() 实时扣款
  - 扣款Bill关联 SessionID（已有）

DistributePenaltyFromPlatform（分发罚金）:
  - 变更: 不再调用 platform.Settle()
  - 改为调用 platform.Credit()（只入账，不上报结算）
  - 入账Bill仍关联原 SessionID
  - 罚金金额自动参与游戏级聚合（按 SessionID + UserID 汇总到该玩家的 PayOut）
```

### 6.4 奖励结算

```
当前: 独立调用 platform.Settle()
重构后:
  - 回合结束时: 调用 platform.Credit() 入账（与其他入账一致）
  - 游戏结算时: 奖励金额自动按 SessionID + UserID 聚合到该玩家的 PayOut
  - 奖励不再单独调用 platform.Settle()
```

### 6.5 首回合扣款失败自动退款

```
当前逻辑: DeductForFirstRound 部分失败 → 自动 Refund 已扣款玩家
重构后: 逻辑不变，退款仍调用 platform.Credit()（当前已经是Credit）
  - 退款Bill关联 SessionID（已有）
  - 退款金额不参与游戏级聚合（退款BillType不同，不在聚合范围内）
```

---

## 七、调度器变更

### 7.1 适配

| 调度器 | 变更 |
|---|---|
| **CreditRetryScheduler** | 重试目标从 `platform.Settle` 改为 `platform.Credit`；重试对象仍是失败的入账BillRecord |
| **ExceptionHandleScheduler** | `SettlementMissing` 异常处理逻辑适配：检查该 SessionID 的游戏级结算是否完成；对于已扣款但游戏未正常结算的玩家触发退款 |
| **PairBillCheckScheduler** | 扣款侧对账不变；入账侧检查 Credit bill 与 Debit bill 的配对关系；新增按 SessionID 的游戏级对账（BetAmount 总和 vs PayOut 总和 + 佣金） |
| **SettlementCheckScheduler** | 从检查 RoundSettlement 完整性改为检查同一 SessionID 下所有回合是否完成 + 游戏级结算是否已上报 |

### 7.2 新增

| 调度器 | 间隔 | 职责 |
|---|---|---|
| **GameSettleTimeoutScheduler** | 5min | 扫描超时未结算的游戏（RoundSettlement.GameSettleStatus=0 且距最后回合完成 > 1h），触发强制结算或异常告警 |
| **GameSettleRetryScheduler** | 30s | 扫描 GameSettleStatus=Failed 的游戏，对未结算玩家（BillRecord.GameSettleStatus=0 的入账Bill）重新调用 platform.Settle(/settle) |

---

## 八、Redis Key 变更

### 8.1 保留（扣款和回合级入账仍需）

```
cashparty:settle:lock:first_round:{sessionID}   — 首回合批量扣款锁
cashparty:settle:lock:later_round:{roundID}     — 后续回合扣款锁
cashparty:settle:lock:system_packet:{roundID}   — 系统红包扣款锁
cashparty:settle:lock:deduct:{roundID}_{billType}_{userID}  — 单笔扣款锁
cashparty:settle:lock:send_packet:{roundID}     — 发红包锁
cashparty:settle:lock:penalty:{roundID}         — 罚金锁
cashparty:settle:lock:bill_retry:{billID}       — 入账重试锁
```

### 8.2 新增（游戏级结算）

```
cashparty:settle:lock:game:{sessionID}  — 游戏结算分布式锁（防止多实例并发结算同一局游戏）
```

幂等性通过数据库 `RoundSettlement.GameSettleStatus` 判断，无需额外 Redis 完成标记。

### 8.3 可移除

```
cashparty:settle:lock:round:{roundID}  — 原用于回合级Settle，拆分后回合只做Credit，不需要此锁
cashparty:settle:done:{roundID}        — 原标记回合Settle完成，改为标记回合Credit完成
```

---

## 九、受影响文件清单

### 9.1 新增文件

| 文件 | 说明 |
|---|---|
| `service/game_settle_service.go` | 游戏级结算核心逻辑（聚合BillRecord + 调用 platform.Settle） |
| `scheduler/game_settle_timeout_scheduler.go` | 游戏结算超时调度器 |
| `scheduler/game_settle_retry_scheduler.go` | 游戏结算重试调度器 |

### 9.2 需修改文件

| 文件 | 修改内容 |
|---|---|
| `api/platform/gamingpanda_client.go` | `Settle` 端点从 `/credit_n_settle` 改为 `/settle` |
| `api/platform/mock_client.go` | `Settle` 实现改为仅记录游戏结果，不修改余额 |
| `dto/constants.go` | 新增 RoundStatusCredited、GameSettleStatus、BillGameSettleStatus 枚举 |
| `dto/request.go` | 新增 GameSettleRequest；RoundSettleRequest 简化（去掉结算相关字段） |
| `dto/response.go` | 新增 GameSettleInfo 响应结构 |
| `model/bill.go` | BillRecord 增加 GameSettleStatus 字段 |
| `model/round_settlement.go` | RoundSettlement 增加 GameSettleStatus、GameSettledAt 字段 |
| `service/settlement_service.go` | **核心重构**：SettleRound 拆分为 CreditRound（调用 platform.Credit）+ 检查触发游戏结算；删除 executeCredit（改为 CreditRound）；新增 SettleGame 方法（聚合 + 调用 platform.Settle(/settle)） |
| `service/deduct_service.go` | 小改：扣款Bill写入 SessionID（已有字段） |
| `service/credit_retry_service.go` | 重试目标从 platform.Settle 改为 platform.Credit |
| `service/bill_manager.go` | 新增按 SessionID 聚合金额的方法（AggregateBetBySession、AggregatePayOutBySession） |
| `scheduler/credit_retry_scheduler.go` | 适配 Credit 重试（非 Settle 重试） |
| `scheduler/exception_handle_scheduler.go` | 异常处理适配游戏级结算 |
| `scheduler/pair_bill_check_scheduler.go` | 对账逻辑适配 |
| `scheduler/settlement_check_scheduler.go` | 检查逻辑适配 |
| `infrastructure/persistence/redis/keys.go` | 新增游戏级 Key，移除回合级 Settle Key |

### 9.3 逻辑删除（不再需要）

| 逻辑 | 原文件 | 说明 |
|---|---|---|
| `executeCredit` 中的 bet_amount/payout/game_result 计算 | settlement_service.go | 不再需要回合级拼凑，改由游戏级聚合 BillRecord |
| `settleCommission` 中的回合级调用 | settlement_service.go | 移入 CreditRound，仍是纯记账 |
| 回合级 `platform.Settle()` 调用 | settlement_service.go / credit_retry_service.go | 全部替换为 `platform.Credit()`；游戏级结算使用 `platform.Settle(/settle)` |

---

## 十、数据流对比

### 10.1 当前（每回合 Settle）

```
Round 1:  Debit → Bill(deduct)          →  Settle(bet, payout, result) → Bill(credit)
Round 2:  Debit → Bill(deduct)          →  Settle(bet, payout, result) → Bill(credit)
...
Round 10: Debit → Bill(deduct)          →  Settle(bet, payout, result) → Bill(credit)

平台API: 10×Debit + 10×Settle × N玩家
每笔Settle的bet_amount语义模糊
```

### 10.2 重构后（回合Credit + 游戏Settle）

```
Round 1:  Debit(/debit) → Bill(deduct)  →  Credit(/credit) → Bill(credit)
Round 2:  Debit(/debit) → Bill(deduct)  →  Credit(/credit) → Bill(credit)
...
Round 10: Debit(/debit) → Bill(deduct)  →  Credit(/credit) → Bill(credit)

Game End: 聚合BillRecords → Settle(/settle, 总bet, 总payout, result)

平台API: 10×Debit + 10×Credit × N玩家 + 1×Settle × N玩家
每笔Settle的bet_amount = 玩家整局真实投入，且不移动资金
```

**调用次数对比**：

| API | 当前 | 重构后 |
|---|---|---|
| Debit | 10 × N（/debit） | 10 × N（/debit，不变） |
| Credit | 0 | 10 × N（/credit，新增，替代Settle的入账部分） |
| Settle | 10 × N（/credit_n_settle，入账+结算） | 0（不再使用） |
| Settle | 10 × N（/credit_n_settle，入账+结算） | 1 × N（/settle，仅结算上报，不移动资金） |

**总结**：总平台调用次数从 `10×Debit + 10×Settle` 变为 `10×Debit + 10×Credit + 1×Settle`。关键变化是 `/credit_n_settle`（入账+结算耦合）拆分为 `/credit`（只入账）+ `/settle`（只结算），结算调用减少 90%，且 bet_amount 语义正确。

---

## 十一、platform.Client 接口变更

### 11.1 修改 Settle 端点

将 `Settle()` 的端点从 `/credit_n_settle` 改为 `/settle`，语义从"入账+结算"变为"只结算不入账"：

| 文件 | 修改 |
|---|---|
| `api/platform/client.go` | 接口不变（签名仍是 `Settle(ctx, *SettleRequest)`） |
| `api/platform/gamingpanda_client.go` | 端点从 `/credit_n_settle` 改为 `/settle` |
| `api/platform/mock_client.go` | `Settle` 实现改为仅记录游戏结果，不修改余额 |

### 11.2 调用方变更

| 场景 | 当前调用 | 重构后调用 |
|---|---|---|
| 回合级入账 | `platform.Settle(/credit_n_settle)` | `platform.Credit(/credit)` |
| 游戏级结算 | 不存在 | `platform.Settle(/settle)` |
| 退款 | `platform.Credit(/credit)`（不变） | `platform.Credit(/credit)`（不变） |
| 扣款 | `platform.Debit(/debit)`（不变） | `platform.Debit(/debit)`（不变） |

---

## 十二、风险评估

| 风险 | 影响 | 缓解 |
|---|---|---|
| 风险 | 影响 | 缓解 |
|---|---|---|
| 游戏中断，部分回合未入账 | 玩家资金滞留 | GameSettleTimeoutScheduler 强制结算或退款 |
| 入账失败影响游戏结算 | 部分玩家数据缺失 | 游戏结算时只聚合已成功的 Credit Bill，失败的由 CreditRetryScheduler 处理后再重试游戏结算 |
| Credit 和 Settle 时序问题 | 入账成功但结算未上报 | GameSettleRetryScheduler 保证最终上报 |
| 重构期间新旧逻辑兼容 | 线上故障 | 灰度：新房间走新逻辑，旧房间走旧逻辑，通过 RoomID 或创建时间区分 |

---

## 十三、预期收益

1. **语义正确**：bet_amount = 真实总投入，payout = 真实总收入，result = 真实输赢
2. **合规提升**：消除"下注为0赢了钱"的监管风险
3. **逻辑简化**：不再需要 isMin/senderType/roomFeePerPlayer 的复杂 bet_amount 拼凑逻辑
4. **性能优化**：Settle 调用减少 90%，降低平台压力和失败率
5. **可审计**：每笔 Settle 对应一个完整游戏，审计追踪更清晰
6. **关注点分离**：入账（资金流）和结算（业务流）解耦，各自可独立重试和排查
