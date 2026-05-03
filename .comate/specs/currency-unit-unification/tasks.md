# 金额单位统一重构任务计划

- [x] Task 1: 创建 `common/currency` 包核心代码
    - 1.1: 创建 `backend/common/currency/money.go`，实现 `Money` 类型及 `MarshalJSON`/`UnmarshalJSON`/`Fen`/`Yuan`/`String`/`NewMoneyFromFen`/`NewMoneyFromYuan`
    - 1.2: 创建 `backend/common/currency/convert.go`，迁移 `FormatAmount`/`ParseAmount` 函数
    - 1.3: 创建 `backend/common/currency/money_test.go`，覆盖正数/零/负数序列化、反序列化、ParseAmount/FormatAmount 兼容性测试

- [x] Task 2: 兼容性适配 `api/platform/utils.go`
    - 2.1: 将 `FormatAmount`/`ParseAmount` 改为委托到 `currency` 包的变量赋值，保持现有调用方零改动

- [x] Task 3: 更新 WebSocket 消息类型 `common/message/payload.go`
    - 3.1: 将 `RoundResult.Amount`、`GameResult.TotalProfit` 改为 `currency.Money`
    - 3.2: 将 `RoundStartPush` 的 `TotalAmount`/`Commission`/`ActualAmount` 改为 `currency.Money`
    - 3.3: 将 `PacketGrabbedPush.Amount` 改为 `currency.Money`
    - 3.4: 将 `RoundEndPush` 的 `TotalAmount`/`Commission`/`RewardAmount` 改为 `currency.Money`
    - 3.5: 将 `DistributeResult.Amount` 改为 `currency.Money`
    - 3.6: 将 `PenaltyPush.PenaltyAmount` 改为 `currency.Money`
    - 3.7: 将 `GameInterruptedPush.PenaltyShare` 改为 `currency.Money`

- [x] Task 4: 更新游戏服务中消息构造代码
    - 4.1: 更新 `game/application/game_service.go` 中构造 `RoundStartPush`/`RoundEndPush` 的赋值，int64 → `currency.Money`
    - 4.2: 更新 `game/application/grab_service.go` 中构造 `PacketGrabbedPush` 的赋值
    - 4.3: 更新 `game/application/penalty_service.go` 中构造 `PenaltyPush` 的赋值
    - 4.4: 更新 `game/application/seat_service.go` 中构造 `AutoDistributePush`/`DistributeResult` 的赋值
    - 4.5: 更新 `settlement/service/reward_settler.go` 中 `RewardAmount` 相关赋值

- [x] Task 5: 更新 Stats API DTO 及服务代码
    - 5.1: 更新 `stats/dto/stats_dto.go` 中所有金额字段为 `currency.Money`
    - 5.2: 检查 `stats/service/stats_service.go` 及 `stats/repository/` 中构造 DTO 的代码，适配 int64 → `currency.Money` 赋值

- [x] Task 6: 更新前端 Dashboard 代码

- [x] Task 7: 编译验证与回归测试
    - 8.1: 全项目 `go build` 编译通过
    - 8.2: 运行 `go test ./common/currency/...` 确保 Money 类型单元测试通过
    - 8.3: 运行 `go test ./...` 确保无回归
