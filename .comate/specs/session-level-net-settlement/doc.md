# 结算模块重构方案

## 一、重构目标

1. **解决核心业务问题**：将逐轮入账改为会话级净额入账，满足"先下注后入账"合规要求
2. **删除无用代码**：移除不再需要的方法、服务、调度器
3. **修复事务一致性**：内部记账全部事务化
4. **修复 Dual Wiring**：消除 bootstrap 中重复创建服务实例的问题
5. **精简服务依赖**：移除不再使用的字段和构造参数

## 二、删除清单

### 2.1 删除的服务方法

| 文件 | 删除项 | 原因 |
|------|--------|------|
| `settlement_service.go` | `executeCreditOnly()` | 改为内部记账后，无调用方 |
| `settlement_service.go` | 字段 `creditRetrySvc` | 仅 `executeCreditOnly()` 使用，删除后无引用 |
| `settlement_service.go` | 字段 `refundSvc` | 未被任何方法使用 |
| `settlement_service.go` | 构造参数 `creditRetrySvc`, `refundSvc` | 随字段删除 |
| `reward_settler.go` | 字段 `platform` | 不再调用 `platform.Credit()` |
| `reward_settler.go` | 字段 `userIDConvert` | 不再调用 `platform.Credit()` |
| `reward_settler.go` | 构造参数 `platformClient`, `userIDConvert` | 随字段删除 |
| `reward_settler.go` | `SettleReward()` 中 `platform.Credit()` 及相关变量 | 改为内部记账 |

### 2.2 删除的服务文件

| 文件 | 原因 |
|------|------|
| `service/pair_bill_check_service.go` | 重构后 BillType 10/11 玩家账单直接 Success，BillType 8/9 也无失败可能，成对检查不再需要 |

### 2.3 删除的调度器

| 文件 | 原因 |
|------|------|
| `scheduler/pair_bill_check_scheduler.go` | 随 PairBillCheckService 删除 |
| `scheduler/manager.go` | 从未被使用，Container 直接管理调度器 |

### 2.4 删除的 Container 字段

| 字段 | 原因 |
|------|------|
| `PairBillCheckScheduler` | 随调度器删除 |

### 2.5 删除的 BillManager 方法

| 方法 | 原因 |
|------|------|
| `GetDebitSuccessCreditFailedBills()` | 仅 PairBillCheckService 使用 |
| `GetCreditBillByRoundTraceID()` | 仅 PairBillCheckService 使用 |

## 三、修改清单

### 3.1 `dto/constants.go`

新增：
```go
BillTypeNetSettlement = 12  // 会话级净额入账
```

### 3.2 `settlement_service.go` — `creditRound()`

**改为内部记账 + 事务**：

- 所有 BillTypeGrabPacket 账单直接标记 `BillStatusSuccess`，不调用 `platform.Credit()`
- 使用 `CreateBillsAndUpdateSettlement()` 事务方法，保证账单创建和轮次状态更新原子性
- 删除 `allSuccess` / `lastError` 跟踪逻辑
- 账单 Remark 改为 `"抢红包收入(待局级净额结算),局ID:%d"`

### 3.3 `settlement_service.go` — `DistributePenaltyFromPlatform()`

**改为事务 + 内部记账**：

- 玩家分红账单直接标记 `BillStatusSuccess`，不调用 `executeCreditOnly()`
- 平台账单 + 所有玩家分红账单通过 `CreateBillsInTransaction()` 事务创建

### 3.4 `reward_settler.go` — `SettleReward()`

**改为事务 + 内部记账**：

- 玩家奖励账单直接标记 `BillStatusSuccess`，不调用 `platform.Credit()`
- 平台支出 + 所有玩家奖励账单通过 `CreateBillsInTransaction()` 事务创建
- 删除 `playerPlatformUserID`, `creditReq`, `creditResult`, `balanceAfter` 等变量

### 3.5 `game_settle_service.go` — 新增净额入账

**新增三个方法**：

#### `netSettlePlayers(ctx, sessionID, betMap, payOutMap)`
- 合并 betMap 和 payOutMap 的所有用户
- 跳过 PlatformAccountID 和净额 <= 0 的玩家
- 对净额 > 0 的玩家调用 `netSettlePlayer()`

#### `netSettlePlayer(ctx, sessionID, userID, netAmount)`
- 幂等检查：通过 `GetBillsBySessionTypeAndUser()` 查找已有 BillTypeNetSettlement 账单
- 已有 Success → 直接返回
- 已有 Processing/Failed → 调用 `executeNetCredit()` 重试
- 不存在 → 创建 BillTypeNetSettlement 账单(Processing)，调用 `executeNetCredit()`

#### `executeNetCredit(ctx, bill)`
- 调用 `platform.Credit()` 入账
- 成功 → 标记 BillStatusSuccess
- 失败 → 标记 BillStatusFailed + 设置 next_retry_at，由 CreditRetryScheduler 重试

**修改 `SettleGame()`**：

在 `AggregateBetBySession()` / `AggregatePayOutBySession()` 之后、`settlePlayer()` 之前，插入：
```go
if err := s.netSettlePlayers(ctx, sessionID, betMap, payOutMap); err != nil {
    logger.Error("net settle players failed", "session_id", sessionID, "error", err)
}
```

### 3.6 `bill_manager.go`

**修改**：
- `AggregatePayOutBySession()`：排除 `BillTypeNetSettlement`，避免重复计算

**新增**：
- `GetBillsBySessionTypeAndUser(ctx, sessionID, billType, userID)` — 净额入账幂等检查
- `CreateBillsAndUpdateSettlement(ctx, roundTraceID, settleAmount, settleUserCount, settledAt, bills)` — 事务：批量创建账单 + 更新轮次状态

### 3.7 `credit_retry_service.go` — `executeCredit()`

修改 RoundID 参数，对 `BillTypeNetSettlement` 使用 `SessionID` 替代 `RoundID=0`：
```go
roundIDForPlatform := fmt.Sprintf("%d", bill.RoundID)
if bill.BillType == dto.BillTypeNetSettlement {
    roundIDForPlatform = fmt.Sprintf("%d", bill.SessionID)
}
```

### 3.8 `settlement_service.go` — `NewSettlementService()`

- 删除参数 `creditRetrySvc`, `refundSvc`
- 删除字段赋值
- 其他参数和字段保持不变

### 3.9 `reward_settler.go` — `NewRewardSettler()`

- 删除参数 `platformClient`, `userIDConvert`
- 删除字段赋值
- 其他参数和字段保持不变

### 3.10 `bootstrap/app.go`

修复 Dual Wiring，统一使用单一实例：
- 删除 `app.go` 中 `creditRetrySvc`, `deductSvc`, `refundSvc`, `rewardSettler`, `gameSettleSvc`, `callMgr` 的创建
- `NewSettlementService()` 和 `NewContainer()` 的参数同步更新
- 所有服务实例统一在 `container.go` 的 `InitAppServices()` 中创建

### 3.11 `bootstrap/container.go`

- 删除 `PairBillCheckScheduler` 字段
- `initSettlementSchedulers()` 中删除 `PairBillCheckService` 和 `PairBillCheckScheduler` 的创建
- `StartSchedulers()` / `Stop()` 中删除 `PairBillCheckScheduler`
- 统一服务实例创建，消除 dual wiring

## 四、保留清单（不变）

### 4.1 服务

| 文件 | 说明 |
|------|------|
| `deduct_service.go` | 扣款逻辑不变 |
| `refund_service.go` | 退款流程不变（首回合失败、异常退款等仍需要） |
| `balance_service.go` | 余额查询不变 |
| `exception_manager.go` | 异常管理不变 |
| `settlement_check_service.go` | 结算检查不变（首回合失败检测、扣款未结算检测） |
| `platform_call_manager.go` | 平台调用日志不变（仅 game_settle_service 使用） |
| `trace_id_generator.go` | 不变 |
| `user_id_convert_service.go` | 不变 |

### 4.2 调度器

| 调度器 | 频率 | 保留原因 |
|--------|------|----------|
| `CreditRetryScheduler` | 30s | 重试净额入账(BillType=12)失败账单 |
| `SettlementCheckScheduler` | 5min | 检测首回合扣款失败和扣款未结算 |
| `RefundProcessScheduler` | 1min | 自动审批首回合失败退款 |
| `ExceptionHandleScheduler` | 5min | 处理异常记录（结算缺失→自动退款） |
| `GameSettleRetryScheduler` | 30s | 重试游戏级结算失败 |
| `GameSettleTimeoutScheduler` | 5min | 强制结算超时未完成的会话 |

### 4.3 模型

| 模型 | 说明 |
|------|------|
| `BillRecord` | 无需新增字段，现有结构足够 |
| `RoundSettlement` | 不变 |
| `RefundAudit` | 不变 |
| `ExceptionRecord` | 不变 |

## 五、事务一致性

### 5.1 事务原则

| 操作类型 | 策略 |
|----------|------|
| 内部记账（仅创建 BillRecord，不调平台） | **数据库事务** — 纯本地操作，必须原子性 |
| 净额入账（创建 BillRecord + platform.Credit） | **先创建 Processing 账单，再调平台，失败由重试保障** |
| 扣款（Debit） | 不变，已有事务保障 |

### 5.2 creditRound() 事务

将所有 BillRecord 创建 + RoundSettlement 状态更新放在一个事务内（`CreateBillsAndUpdateSettlement`）。

### 5.3 DistributePenaltyFromPlatform() 事务

平台账单 + 所有玩家分红账单通过 `CreateBillsInTransaction()` 事务创建。

### 5.4 SettleReward() 事务

平台支出 + 所有玩家奖励账单通过 `CreateBillsInTransaction()` 事务创建。

### 5.5 netSettlePlayer() 策略

无法用数据库事务包裹外部 API 调用。采用：
1. 先创建 BillStatusProcessing 账单
2. 调用 platform.Credit()
3. 成功 → 标记 Success；失败 → 标记 Failed + 设置 next_retry_at
4. CreditRetryScheduler 自动重试

## 六、数据流（重构后）

```
游戏开始
  │
  ▼
Round 1: DeductForFirstRound() → platform.Debit() x5 ✅ 实时扣款
  │
  ▼ 抢红包
  │
Round 1 结算: SettleRound() → creditRound()
  │  事务创建 5 条 BillTypeGrabPacket 账单 → 直接 Success（内部记账）
  │  事务创建 1 条 BillTypeCommission 账单 → 直接 Success
  │  轮次标记 RoundStatusCredited
  │
  ▼
Round 2+: DeductForLaterRound() → platform.Debit() x1 ✅ 实时扣款
  │
  ▼ 抢红包
  │
Round 2+ 结算: SettleRound() → creditRound()
  │  事务创建 5 条 BillTypeGrabPacket 账单 → 直接 Success（内部记账）
  │  轮次标记 RoundStatusCredited
  │
  ▼ (如有奖励)
  │  RewardSettler.SettleReward()
  │  事务创建平台支出 + 玩家奖励账单 → 全部直接 Success
  │
  ▼ (如有惩罚分配)
  │  DistributePenaltyFromPlatform()
  │  事务创建平台支出 + 玩家分红账单 → 全部直接 Success
  │
  ▼
会话结束: SettleGame()
  │  聚合: betMap, payOutMap（排除 BillType=12）
  │
  │  ★ 净额入账: netSettlePlayers()
  │    对每个玩家: netAmount = payout - bet
  │    if netAmount > 0: 创建 BillTypeNetSettlement 账单 → platform.Credit(netAmount) ✅
  │
  │  报告结果: settlePlayer()
  │    platform.Settle(betAmount, payOut, result) ✅
  │
  ▼
结算完成
```

## 七、重构后文件结构

```
settlement/
├── config/
│   └── config.go
├── dto/
│   ├── constants.go            # +BillTypeNetSettlement=12
│   ├── request.go
│   └── response.go
├── infrastructure/persistence/
│   ├── redis/keys.go
│   └── persistence/redis/
├── model/
│   ├── bill.go
│   ├── exception_record.go
│   ├── platform_settle_log.go
│   └── refund.go
├── scheduler/
│   ├── base.go                 # 保留
│   ├── credit_retry_scheduler.go         # 保留
│   ├── settlement_check_scheduler.go     # 保留
│   ├── refund_process_scheduler.go       # 保留
│   ├── exception_handle_scheduler.go     # 保留
│   ├── game_settle_retry_scheduler.go    # 保留
│   ├── game_settle_timeout_scheduler.go  # 保留
│   ❌ pair_bill_check_scheduler.go       # 删除
│   ❌ manager.go                         # 删除
└── service/
    ├── balance_service.go      # 不变
    ├── bill_manager.go         # 修改+新增方法
    ├── credit_retry_service.go # 修改 executeCredit()
    ├── deduct_service.go       # 不变
    ├── exception_manager.go    # 不变
    ├── game_settle_service.go  # 修改+新增方法
    ❌ pair_bill_check_service.go          # 删除
    ├── platform_call_manager.go          # 不变
    ├── refund_service.go       # 不变
    ├── reward_settler.go       # 修改（精简依赖+事务+内部记账）
    ├── settlement_check_service.go       # 不变
    ├── settlement_service.go   # 修改（精简依赖+内部记账+删除方法）
    ├── trace_id_generator.go   # 不变
    └── user_id_convert_service.go        # 不变
```
