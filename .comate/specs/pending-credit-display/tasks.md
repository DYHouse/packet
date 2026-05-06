# 待入账金额实时展示

- [ ] Task 1: 在 `game/application/user_service.go` 中新增 `GetPendingCredit` 方法
    - 1.1: 新增 import（`strconv`、`redis` key 包已在引用中）
    - 1.2: 实现 `GetPendingCredit(ctx, userID string) int64`，按顺序查询：PlayerRoomKey → RoomHashKey HGETALL → 校验 status==Playing(2) → 获取 sessionID → SessionPlayerTotalsKey HGET → 返回累计金额，任何一步无数据则返回 0

- [ ] Task 2: 修改 `game/server/generic_service.go` 的 `handleGetUserBalance`
    - 2.1: 在获取 balance 之后，调用 `s.userSvc.GetPendingCredit(ctx, req.UserId)` 获取 pendingCredit
    - 2.2: 响应中增加 `"pending_credit": currency.NewMoneyFromFen(pendingCredit)` 字段

- [ ] Task 3: 编译验证
    - 3.1: 运行 `go build ./game/...` 确保编译通过
