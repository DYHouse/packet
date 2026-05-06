# 待入账金额实时展示 - 完成总结

## 修改内容

### 1. `game/application/user_service.go`
- 新增 `GetPendingCredit(ctx, userID) int64` 方法
- 查询链路：`PlayerRoomKey(userID)` → `RoomHashKey(roomID)` HGETALL → 校验 status==Playing(2) → `SessionPlayerTotalsKey(sessionID)` HGET → 返回累计金额
- 任何一步无数据或游戏未进行中，返回 0
- 新增 import `strconv`

### 2. `game/server/generic_service.go`
- `handleGetUserBalance` 中调用 `userSvc.GetPendingCredit` 获取待入账金额
- 响应新增 `"pending_credit"` 字段，类型 `currency.Money`

## 验证
- `go build ./game/...` 编译通过
