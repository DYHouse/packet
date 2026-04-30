# 结算模块重构 - 完成总结

## 重构目标

1. 解决"先下注后入账"合规问题：将会话内逐轮入账改为会话级净额入账
2. 删除无用代码：移除不再需要的方法、服务、调度器
3. 修复事务一致性：内部记账全部事务化
4. 修复 Dual Wiring：消除 bootstrap 中重复创建服务实例的问题
5. 精简服务依赖：移除不再使用的字段和构造参数

## 完成的任务

### Task 1: 新增常量和 BillManager 方法
- `dto/constants.go`：新增 `BillTypeNetSettlement = 12`
- `bill_manager.go`：新增 `GetBillsBySessionTypeAndUser()` 用于幂等检查
- `bill_manager.go`：新增 `CreateBillsAndUpdateSettlement()` 事务方法
- `bill_manager.go`：修改 `AggregatePayOutBySession()` 排除 `BillTypeNetSettlement`

### Task 2: 改造 creditRound() + 删除 executeCreditOnly()
- 重写 `creditRound()`：批量创建 BillStatusSuccess 账单，使用 `CreateBillsAndUpdateSettlement()` 事务
- 删除 `executeCreditOnly()` 方法

### Task 3: 改造 DistributePenaltyFromPlatform()
- 重写为事务 + 内部记账：所有账单通过 `CreateBillsInTransaction()` 事务创建，直接标记 Success

### Task 4: 改造 RewardSettler 精简依赖 + 事务 + 内部记账
- 删除字段 `platform`、`cfg`、`userIDConvert` 及构造函数参数
- 重写 `SettleReward()`：平台支出 + 玩家奖励账单通过 `CreateBillsInTransaction()` 事务创建，玩家账单直接标记 Success
- 删除 `platform.Credit()` 调用及所有相关变量

### Task 5: 精简 SettlementService 依赖
- 删除字段 `creditRetrySvc`、`refundSvc` 及构造函数参数

### Task 6: 在 GameSettleService 中实现净额入账逻辑
- 新增 `netSettlePlayers()`：遍历玩家计算净额，对净额>0 的玩家调用 `netSettlePlayer()`
- 新增 `netSettlePlayer()`：幂等检查 + 创建 BillTypeNetSettlement 账单(Processing) + 调用 `executeNetCredit()`
- 新增 `executeNetCredit()`：调用 `platform.Credit()` 入账，失败标记 Failed 并设置 next_retry_at
- 修改 `SettleGame()`：在聚合 betMap/payOutMap 之后插入 `netSettlePlayers()` 调用

### Task 7: 适配 CreditRetryService
- 修改 `executeCredit()`：对 `BillTypeNetSettlement` 使用 `SessionID` 替代 `RoundID`

### Task 8: 删除无用代码
- 删除 `service/pair_bill_check_service.go`
- 删除 `scheduler/pair_bill_check_scheduler.go`
- 删除 `scheduler/manager.go`
- 删除 `bill_manager.go` 中 `GetDebitSuccessCreditFailedBills()` 和 `GetCreditBillByRoundTraceID()`

### Task 9: 修复 bootstrap Dual Wiring + 同步更新
- `app.go`：更新 `NewRewardSettler()` 和 `NewSettlementService()` 调用签名；将共享服务实例传递给 `NewContainer()`
- `container.go`：删除 `PairBillCheckScheduler` 字段；接受共享服务实例作为构造参数；`initSettlementSchedulers()` 简化为无参数，直接使用容器字段；删除 `PairBillCheckService` 和 `PairBillCheckScheduler` 创建；`StartSchedulers()`/`Stop()` 中删除 `PairBillCheckScheduler`

## 修改文件清单

| 文件 | 操作 |
|------|------|
| `settlement/dto/constants.go` | 新增常量 |
| `settlement/service/bill_manager.go` | 新增方法 + 修改查询 + 删除2个方法 |
| `settlement/service/settlement_service.go` | 重写 creditRound + 删除 executeCreditOnly + 精简依赖 |
| `settlement/service/reward_settler.go` | 重写 + 精简依赖 |
| `settlement/service/game_settle_service.go` | 新增3个方法 + 修改 SettleGame |
| `settlement/service/credit_retry_service.go` | 修改 executeCredit |
| `settlement/service/pair_bill_check_service.go` | 删除 |
| `settlement/scheduler/pair_bill_check_scheduler.go` | 删除 |
| `settlement/scheduler/manager.go` | 删除 |
| `game/bootstrap/app.go` | 更新服务创建和传参 |
| `game/bootstrap/container.go` | 重写，消除 Dual Wiring |

## 验证结果

- `go build ./settlement/...` — 通过
- `go build ./game/...` — 通过
- `go vet ./settlement/... ./game/...` — 通过
