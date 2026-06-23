# Tasks

- [ ] Task 1: 新增 LuaJoinAndAutoSeat 原子脚本及仓储方法
  - [ ] SubTask 1.1: 在 `keys.go` 中新增排队队列 Key 常量和函数 `RoomQueueKey`
  - [ ] SubTask 1.2: 在 `lua_scripts.go` 中新增 `LuaJoinAndAutoSeat` 脚本：加入观战者 + 检测空座 + 自动分配最低编号空座 + 自动准备（转为player），满座或余额不足时仅加入观战者
  - [ ] SubTask 1.3: 在 `domain/repository.go` 中新增 `JoinAndAutoSeat` 接口方法和 `JoinAndAutoSeatResult` 数据结构
  - [ ] SubTask 1.4: 在 `redis/repository.go` 中实现 `JoinAndAutoSeat` 方法，调用 LuaJoinAndAutoSeat 脚本并解析返回值

- [ ] Task 2: 修改 JoinRoom 应用服务实现自动上座
  - [ ] SubTask 2.1: 修改 `JoinRoomResult` 结构体，新增 `SeatNo` 字段
  - [ ] SubTask 2.2: 修改 `JoinRoom` 方法：先检查余额，再调用 `JoinAndAutoSeat`，根据返回结果设置 is_spectator 和 seat_no
  - [ ] SubTask 2.3: 修改 `AutoMatchAndJoin` 方法适配新逻辑
  - [ ] SubTask 2.4: 修改 `generic_service.go` 中 `handleJoinRoom` 和 `handleAutoMatch` 的响应，包含 seat_no 字段

- [ ] Task 3: 新增排队相关 Lua 脚本及仓储方法
  - [ ] SubTask 3.1: 在 `lua_scripts.go` 中新增 `LuaEnqueue` 脚本：将观战者加入排队有序集合，检查是否已在队列中
  - [ ] SubTask 3.2: 在 `lua_scripts.go` 中新增 `LuaDequeue` 脚本：将观战者从排队队列中移除
  - [ ] SubTask 3.3: 在 `lua_scripts.go` 中新增 `LuaAutoSubstitute` 脚本：从队列头部取出排队者，分配指定座位，转为player，从队列移除
  - [ ] SubTask 3.4: 在 `domain/repository.go` 中新增 `Enqueue`、`Dequeue`、`AutoSubstitute` 接口方法和相关数据结构
  - [ ] SubTask 3.5: 在 `redis/repository.go` 中实现 `Enqueue`、`Dequeue`、`AutoSubstitute` 方法

- [ ] Task 4: 新增排队/替补事件和命令常量
  - [ ] SubTask 4.1: 在 `domain/events.go` 中新增事件类型 `RoomEventEnqueue`、`RoomEventDequeue`、`RoomEventSubstitute` 及对应 Payload 结构体和构造函数
  - [ ] SubTask 4.2: 在 `common/message/types.go` 中新增命令常量 `CmdEnqueue`、`CmdDequeue` 和推送常量 `PushSubstitute`
  - [ ] SubTask 4.3: 在 `common/message/errors.go` 中新增错误码 `CodeAlreadyInQueue`、`CodeNotInQueue`
  - [ ] SubTask 4.4: 在 `domain/lua_errors.go` 中新增 Lua 错误码 `LuaErrAlreadyInQueue`、`LuaErrNotInQueue`

- [ ] Task 5: 修改 CancelSeat 和 LeaveRoom 触发替补逻辑
  - [ ] SubTask 5.1: 修改 `seat_app_service.go` 的 `CancelSeat` 方法：在释放座位后调用 `AutoSubstitute`，若有替补者则广播替补事件和房间状态
  - [ ] SubTask 5.2: 修改 `room_app_service.go` 的 `LeaveRoom` 方法：在离开房间后，若释放了座位则调用 `AutoSubstitute`；同时从排队队列中移除该用户
  - [ ] SubTask 5.3: 在 `domain/repository.go` 中新增 `RemoveFromQueue` 接口方法
  - [ ] SubTask 5.4: 在 `redis/repository.go` 中实现 `RemoveFromQueue` 方法

- [ ] Task 6: 新增 Enqueue/Dequeue 应用服务与命令路由
  - [ ] SubTask 6.1: 在 `room_app_service.go` 中新增 `Enqueue` 方法：调用 repo.Enqueue，发布事件，广播房间状态
  - [ ] SubTask 6.2: 在 `room_app_service.go` 中新增 `Dequeue` 方法：调用 repo.Dequeue，发布事件，广播房间状态
  - [ ] SubTask 6.3: 在 `generic_service.go` 中新增 `handleEnqueue` 和 `handleDequeue` 处理函数
  - [ ] SubTask 6.4: 在 `generic_service.go` 的 `Forward` switch 中注册 `CmdEnqueue` 和 `CmdDequeue` 分支
  - [ ] SubTask 6.5: 在 `gateway-router.yaml` 中新增 `enqueue` 和 `dequeue` 路由

- [ ] Task 7: 扩展 RoomState DTO 和房间状态数据
  - [ ] SubTask 7.1: 在 `domain/room.go` 中新增 `Queuer` 数据结构（UserID、Nickname、Avatar、QueuedAt）
  - [ ] SubTask 7.2: 在 `domain/repository.go` 的 `RoomStateData` 中新增 `QueueList` 字段
  - [ ] SubTask 7.3: 在 `application/room_state.go` 中新增 `QueueInfo` 结构体和 `QueueList` 字段到 `RoomState`
  - [ ] SubTask 7.4: 修改 `BuildFullRoomState` 函数，填充 QueueList 数据
  - [ ] SubTask 7.5: 在 `domain/repository.go` 中新增 `GetQueueList` 接口方法
  - [ ] SubTask 7.6: 在 `redis/repository.go` 中实现 `GetQueueList` 方法
  - [ ] SubTask 7.7: 修改 `GetRoomStateData` 方法，获取排队列表数据

- [ ] Task 8: 替补时余额检查与异常处理
  - [ ] SubTask 8.1: 在 `AutoSubstitute` 应用逻辑中，替补前检查排队者余额，余额不足时跳过并通知用户
  - [ ] SubTask 8.2: 替补成功后推送 `PushSubstitute` 事件给替补者

- [ ] Task 9: 适配机器人系统
  - [ ] SubTask 9.1: 修改 `RobotPlayer.JoinAndReady()`：调用 `JoinRoom` 后检查返回的 `IsSpectator`，若为 false（已自动上座并准备）则直接返回，不再调度 seat/ready；若为 true（满座）则返回 `ErrNoEmptySeat`
  - [ ] SubTask 9.2: 确认 `RobotPlayer.LeaveRoom()` 与替补逻辑兼容（CancelSeat 释放座位触发替补，LeaveRoom 清理数据并从队列移除）
  - [ ] SubTask 9.3: 确认 `recycleZombieRobots` 逻辑兼容（自动上座后机器人立即在 Players 集合中，僵尸检测仍有效）

# Task Dependencies
- Task 1 → Task 2 (JoinAndAutoSeat 脚本和仓储方法需先完成，JoinRoom 才能调用)
- Task 3 → Task 5 (排队Lua脚本和仓储方法需先完成，CancelSeat/LeaveRoom 才能触发替补)
- Task 3 → Task 6 (排队Lua脚本和仓储方法需先完成，Enqueue/Dequeue 才能实现)
- Task 4 → Task 5, Task 6 (事件和命令常量需先定义)
- Task 7 独立于其他任务，可并行开发
- Task 8 依赖 Task 5 和 Task 6
- Task 9 依赖 Task 2 (JoinRoom 返回 IsSpectator 后机器人才能判断是否自动上座)
