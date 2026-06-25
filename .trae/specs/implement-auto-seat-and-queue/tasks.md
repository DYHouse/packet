# Tasks

## 后端：自动上座（模块1+2）

- [ ] Task 1: 新增 `LuaAutoSeatAndReady` 原子脚本（选座+准备）
  - [ ] SubTask 1.1: 在 `backend/game/infrastructure/persistence/redis/lua_scripts.go` 新增脚本，复用现有 `LuaSelectSeat` + `LuaPlayerReady` 核心逻辑，合并为单脚本
  - [ ] SubTask 1.2: 脚本接收 roomID/userID 参数，原子地查找最低编号空座位 + 选座 + 准备（spectator → player）；无空座位时返回"无空座"标记
  - [ ] SubTask 1.3: 在 `backend/game/domain/repository.go` 新增 `AutoSeatAndReady` 接口定义
  - [ ] SubTask 1.4: 在 `backend/game/infrastructure/persistence/redis/repository.go` 实现 `AutoSeatAndReady` 仓储方法
  - [ ] SubTask 1.5: 编写单元测试验证并发安全（多 goroutine 同时调用同一房间）

- [ ] Task 2: 新增 `JoinAndAutoSeat` 应用服务方法并改调入房 handler
  - [ ] SubTask 2.1: 在 `backend/game/application/room_app_service.go` 新增 `JoinAndAutoSeat` 方法，依次执行：调用 `JoinRoom` → 检查 `Status==Playing` → `CheckBalanceForReady` → `AutoSeatAndReady`（处理"无空座"分支返回纯观战）
  - [ ] SubTask 2.2: 修改 `AutoMatchAndJoin` 内部改调 `JoinAndAutoSeat` 而非 `JoinRoom`
  - [ ] SubTask 2.3: 在 `backend/game/server/generic_service.go` 中 `handleJoinRoom`/`handleAutoMatch` 改调 `JoinAndAutoSeat`
  - [ ] SubTask 2.4: 验证 `JoinRoom` 方法签名与逻辑未变，机器人路径不受影响

## 后端：排队预约（模块3）

- [ ] Task 3: 新增排队队列 Redis 数据结构与 Lua 脚本
  - [ ] SubTask 3.1: 在 `backend/game/infrastructure/persistence/redis/keys.go` 新增 `cashparty:room:queue:{roomID}` key 定义（与房间 hash 同步设置 24h TTL）
  - [ ] SubTask 3.2: 在 `lua_scripts.go` 新增 `LuaEnqueue`：校验非机器人 → ZADD 加入有序集合（时间戳 score）→ 返回排队位置
  - [ ] SubTask 3.3: 新增 `LuaDequeue`：ZREM 移除指定排队者
  - [ ] SubTask 3.4: 新增 `LuaAutoSubstitute`：座位释放时从队首取人（跳过机器人）→ 分配释放的座位 → 自动准备 → 从队列移除；队首为机器人则跳过并移除（防御性）
  - [ ] SubTask 3.5: 在 `domain/repository.go` + `redis/repository.go` 新增 Enqueue/Dequeue/AutoSubstitute 仓储方法

- [ ] Task 4: 新增排队/替补事件与 RoomState 扩展
  - [ ] SubTask 4.1: 在 `backend/game/domain/events.go` 新增 `QueueJoin`/`QueueLeave`/`Substitute` 事件类型
  - [ ] SubTask 4.2: 在 `backend/game/domain/room.go` 新增 `QueueInfo` 数据结构（user_id/nickname/avatar/queue_position/queued_at）
  - [ ] SubTask 4.3: 在 `backend/game/application/room_state.go` 的 `RoomState` DTO 新增 `QueueList []QueueInfo`（JSON tag `queue_list`）
  - [ ] SubTask 4.4: 修改 `BuildFullRoomState` 读取队列数据填充 `QueueList`

- [ ] Task 5: 接入排队命令路由与应用服务
  - [ ] SubTask 5.1: 在 `backend/common/message/types.go` 新增 `CmdEnqueue`/`CmdDequeue` 命令常量
  - [ ] SubTask 5.2: 在 `backend/config/gateway-router.yaml` 新增 `enqueue`/`dequeue` 到 game 服务的路由映射
  - [ ] SubTask 5.3: 在 `backend/game/server/generic_service.go` 的 `Forward` switch 新增 `enqueue`/`dequeue` case
  - [ ] SubTask 5.4: 在 `backend/game/application/room_app_service.go` 新增 `Enqueue`/`Dequeue` 方法，调用对应 Lua 脚本并广播 `room_state`

- [ ] Task 6: 座位释放触发自动替补
  - [ ] SubTask 6.1: 在 `backend/game/application/seat_app_service.go` 的 `CancelSeat` 释放座位后调用 `LuaAutoSubstitute`；替补成功后推送 `substitute` 事件
  - [ ] SubTask 6.2: 在 `room_app_service.go` 的 `LeaveRoom` 释放座位后调用 `LuaAutoSubstitute`；同时清理该用户在队列中的位置（若存在）
  - [ ] SubTask 6.3: 在 `backend/game/application/game_app_service.go` 的 `KickPlayerAndInterrupt` 后调用 `LuaAutoSubstitute`；Interrupted 状态下替补补满座位后触发 `ResumeGame`
  - [ ] SubTask 6.4: 替补时若队首排队者余额不足，跳过并通知，继续替补下一位

## 前端：gogain/packages/gift-box（模块4）

- [ ] Task 7: 前端类型与状态扩展
  - [ ] SubTask 7.1: 在 `src/core/systems/room/types.ts` 新增 `QueueInfo` 类型；`RoomStateData` 新增 `queue_list: QueueInfo[]`
  - [ ] SubTask 7.2: 在 `src/core/systems/room/normalizeRoomState.ts` 对 `queue_list` 做空数组兜底

- [ ] Task 8: 前端 WS 命令与事件编排
  - [ ] SubTask 8.1: 在 `src/events/homeEventNames.ts` 的 `HOME_INTERACTION` 新增 `queueEnqueuePress`/`queueDequeuePress`
  - [ ] SubTask 8.2: 在 `src/core/room/roomWsCommands.ts` 新增 `sendEnqueueWs`/`sendDequeueWs`
  - [ ] SubTask 8.3: `registerRoomWsCommandOrchestration` 订阅 `queueEnqueuePress`→`sendEnqueueWs`、`queueDequeuePress`→`sendDequeueWs`
  - [ ] SubTask 8.4: 在 `src/core/room/roomPushPayloads.ts` 新增 `parseSubstitutePayload` 与 `SubstitutePayload` 类型

- [ ] Task 9: 前端 UI 适配
  - [ ] SubTask 9.1: 在 `src/room/widgets/room-bottom-action/RoomBottomAction.ts` 扩展主按钮状态：纯观战满座→"预约排队"、排队中→"取消排队 N"，点击 emit 对应 `HOME_INTERACTION` 事件；原有已入座两态保持不变
  - [ ] SubTask 9.2: 在 `src/room/RoomScreen.ts` 新增排队状态判断（本地用户是否在 `queue_list` 中、房间是否满座），驱动 `RoomBottomAction` 按钮态
  - [ ] SubTask 9.3: 在 `src/room/widgets/room-spectator-list/RoomSpectatorList.ts` 观众列表下方新增排队列表渲染（头像 + 排队位置），订阅 `room.state` 刷新
  - [ ] SubTask 9.4: 在 `src/scene/RoomScene.ts` 的 `room.evt.push` 订阅新增 `case 'substitute'`：本地用户被替补时触发 `ui.cmd.toast`

- [ ] Task 10: 前端 i18n 三语文案
  - [ ] SubTask 10.1: 在 `src/core/i18n/locales/zh.ts` + `en.ts` + `es.ts` 新增：`room.enqueue`/`room.dequeue`/`room.queuePosition`/`room.substitutedToast`

## 协议文档同步

- [ ] Task 11: 同步前后端协议文档
  - [ ] SubTask 11.1: 在 `backend/docs/websocket_protocol.md` 新增 `enqueue`/`dequeue` 命令说明、RoomState `queue_list` 字段、`substitute` 推送类型
  - [ ] SubTask 11.2: 在 `gogain/websocket_protocol.md` 同步上述变更
  - [ ] SubTask 11.3: 在 `gogain/packages/gift-box/docs/room-ws-flow.md` 补充自动上座与排队替补流程图

## 集成验证

- [ ] Task 12: 端到端集成测试
  - [ ] SubTask 12.1: 并发场景测试（多人同时入房争抢座位、多人排队、连续替补）
  - [ ] SubTask 12.2: 边界条件测试（断线重连保留排队位置、余额不足降级、中断状态替补后 ResumeGame）
  - [ ] SubTask 12.3: 机器人兼容性测试（机器人走 `JoinRoom` 原路径不受影响、不排队、调度器与队列协调）

# Task Dependencies

- Task 2 依赖 Task 1（`JoinAndAutoSeat` 调用 `AutoSeatAndReady`）
- Task 5 依赖 Task 3 + Task 4（`Enqueue`/`Dequeue` 调用 Lua 脚本与事件）
- Task 6 依赖 Task 3 + Task 4（触发 `LuaAutoSubstitute` 与 `substitute` 事件）
- Task 8 依赖 Task 7（命令发送依赖类型）
- Task 9 依赖 Task 7 + Task 8（UI 依赖类型与命令/事件）
- Task 11 可与 Task 9 并行（文档与前端实现独立）
- Task 12 依赖所有后端 Task（1-6）与前端 Task（7-10）完成
- 可并行批次：Task 1+3（后端 Lua 脚本）、Task 7+8+10（前端类型/命令/i18n）、Task 11（文档）
