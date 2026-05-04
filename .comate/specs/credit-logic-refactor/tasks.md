# 入账逻辑重构：净额入账 → 抢红包/奖励入账

- [x] Task 1: 重命名常量 BillTypeNetSettlement → BillTypeSessionCredit
    - 1.1: 在 `constants.go` 中将 `BillTypeNetSettlement = 12` 改为 `BillTypeSessionCredit = 12`（值保持不变）
    - 1.2: 全局替换所有引用 `BillTypeNetSettlement` 的代码为 `BillTypeSessionCredit`

- [x] Task 2: 重构 game_settle_service.go 核心入账逻辑
    - 2.1: 将 `netSettlePlayers` 方法重命名为 `creditSessionPayouts`，修改入账金额从 `netAmount(payout-bet)` 改为 `payOut`，条件从 `netAmount <= 0` 改为 `payOut <= 0`
    - 2.2: 将 `netSettlePlayer` 方法重命名为 `creditSessionPayout`，修改入参从 `netAmount` 改为 `payOut`，更新 traceID 前缀、BizOrderNo 前缀、BillType 常量和 Remark
    - 2.3: 将 `executeNetCredit` 方法重命名为 `executeSessionCredit`，无其他逻辑变更
    - 2.4: 在 `SettleGame` 方法中将 `netSettlePlayers` 调用改为 `creditSessionPayouts`，更新相关注释
    - 2.5: 在 `RetryPlayerSettle` 方法中更新注释（如有必要）

- [x] Task 3: 更新 bill_manager.go 聚合查询中的常量引用
    - 3.1: 在 `AggregatePayOutBySession` 中将排除条件 `BillTypeNetSettlement` 改为 `BillTypeSessionCredit`

- [x] Task 4: 更新 settlement_service.go 和 reward_settler.go 中的备注文案
    - 4.1: 在 `creditRound` 方法中将 Remark `"抢红包收入(待局级净额结算)"` 改为 `"抢红包收入(待会话级入账)"`
    - 4.2: 在 `SettleReward` 方法中将 Remark `"系统奖励收入(待局级净额结算)"` 改为 `"系统奖励收入(待会话级入账)"`

- [x] Task 5: 编译验证
    - 5.1: 运行 `go build ./...` 确保所有修改编译通过，无遗漏的引用
