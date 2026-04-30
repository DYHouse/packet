# 结算模块重构 - 任务计划

- [x] Task 1: 新增常量和 BillManager 方法
    - 1.1: 在 `dto/constants.go` 中新增 `BillTypeNetSettlement = 12`
    - 1.2: 在 `bill_manager.go` 中新增 `GetBillsBySessionTypeAndUser()` 方法
    - 1.3: 在 `bill_manager.go` 中新增 `CreateBillsAndUpdateSettlement()` 事务方法
    - 1.4: 修改 `bill_manager.go` 中 `AggregatePayOutBySession()` 排除 `BillTypeNetSettlement`

- [x] Task 2: 改造 `creditRound()` 为内部记账 + 事务
    - 2.1: 重写 `creditRound()`：批量创建 BillStatusSuccess 账单，使用 `CreateBillsAndUpdateSettlement()` 事务
    - 2.2: 删除 `settlement_service.go` 中的 `executeCreditOnly()` 方法

- [x] Task 3: 改造 `DistributePenaltyFromPlatform()` 为事务 + 内部记账
    - 3.1: 重写方法：构建所有账单后通过 `CreateBillsInTransaction()` 事务批量创建，玩家分红账单直接标记 Success
    - 3.2: 删除 `executeCreditOnly()` 调用及相关代码

- [x] Task 4: 改造 `RewardSettler` 精简依赖 + 事务 + 内部记账
    - 4.1: 删除 `RewardSettler` 的字段 `platform`、`userIDConvert` 及构造函数对应参数
    - 4.2: 重写 `SettleReward()`：构建平台支出+玩家奖励账单后通过 `CreateBillsInTransaction()` 事务批量创建，玩家账单直接标记 Success
    - 4.3: 删除 `SettleReward()` 中 `platform.Credit()` 调用及相关变量（`playerPlatformUserID`、`creditReq`、`creditResult`、`balanceAfter`）

- [x] Task 5: 精简 `SettlementService` 依赖
    - 5.1: 删除 `SettlementService` 的字段 `creditRetrySvc`、`refundSvc` 及构造函数对应参数
    - 5.2: 更新 `NewSettlementService()` 签名和实现

- [x] Task 6: 在 `GameSettleService` 中实现净额入账逻辑
    - 6.1: 新增 `netSettlePlayers()` 方法：遍历玩家，计算净额，对净额>0 的玩家调用 `netSettlePlayer()`
    - 6.2: 新增 `netSettlePlayer()` 方法：幂等检查 + 创建 BillTypeNetSettlement 账单 + 调用 `executeNetCredit()`
    - 6.3: 新增 `executeNetCredit()` 方法：调用 `platform.Credit()` 入账，失败设置重试
    - 6.4: 修改 `SettleGame()`：在聚合 betMap/payOutMap 之后、settlePlayer 之前，插入 `netSettlePlayers()` 调用

- [x] Task 7: 适配 `CreditRetryService` 支持净额入账账单重试
    - 7.1: 修改 `executeCredit()` 中 RoundID 参数：对 `BillTypeNetSettlement` 使用 `SessionID` 替代 `RoundID`

- [x] Task 8: 删除无用代码
    - 8.1: 删除 `service/pair_bill_check_service.go` 文件
    - 8.2: 删除 `scheduler/pair_bill_check_scheduler.go` 文件
    - 8.3: 删除 `scheduler/manager.go` 文件
    - 8.4: 删除 `bill_manager.go` 中 `GetDebitSuccessCreditFailedBills()` 方法
    - 8.5: 删除 `bill_manager.go` 中 `GetCreditBillByRoundTraceID()` 方法

- [x] Task 9: 修复 bootstrap Dual Wiring + 同步更新
    - 9.1: 修改 `app.go`：删除 `creditRetrySvc`、`deductSvc`、`refundSvc`、`rewardSettler`、`callMgr`、`gameSettleSvc` 的创建，调整 `NewSettlementService()` 和 `NewContainer()` 调用
    - 9.2: 修改 `container.go`：删除 `PairBillCheckScheduler` 字段，更新 `initSettlementSchedulers()`、`StartSchedulers()`、`Stop()`
    - 9.3: 统一服务实例创建，消除 dual wiring 问题
