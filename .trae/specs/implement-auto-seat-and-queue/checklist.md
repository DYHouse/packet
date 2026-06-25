# Checklist

## 自动上座（模块1+2）
- [ ] `LuaAutoSeatAndReady` 原子脚本实现：查找最低编号空座位 + 选座 + 准备合并为单脚本，无空座时返回"无空座"标记
- [ ] `AutoSeatAndReady` 仓储方法与接口定义实现
- [ ] `JoinAndAutoSeat` 应用服务方法实现：JoinRoom → Status 检查 → 余额校验 → AutoSeatAndReady（含"无空座"分支返回纯观战）
- [ ] `AutoMatchAndJoin` 内部改调 `JoinAndAutoSeat`
- [ ] `handleJoinRoom`/`handleAutoMatch` 改调 `JoinAndAutoSeat`
- [ ] `JoinRoom` 方法签名与逻辑未变（机器人路径零影响验证）
- [ ] 并发入房争抢座位场景验证：`LuaAutoSeatAndReady` 原子性保证安全
- [ ] 余额不足降级为纯观战者验证
- [ ] 游戏进行中入房降级为纯观战者验证

## 排队预约（模块3）
- [ ] Redis 队列 key `cashparty:room:queue:{roomID}` 定义，与房间 hash 同步 24h TTL
- [ ] `LuaEnqueue` 实现：校验非机器人 → ZADD → 返回排队位置
- [ ] `LuaDequeue` 实现：ZREM 移除指定排队者
- [ ] `LuaAutoSubstitute` 实现：队首取人（跳过机器人）→ 分配释放座位 → 自动准备 → 从队列移除
- [ ] `QueueJoin`/`QueueLeave`/`Substitute` 事件类型定义
- [ ] `QueueInfo` 数据结构定义（user_id/nickname/avatar/queue_position/queued_at）
- [ ] `CmdEnqueue`/`CmdDequeue` 命令常量定义
- [ ] `gateway-router.yaml` 新增 `enqueue`/`dequeue` 路由映射
- [ ] `generic_service.go` Forward switch 新增 `enqueue`/`dequeue` case
- [ ] `Enqueue`/`Dequeue` 应用服务方法实现并广播 `room_state`
- [ ] `CancelSeat` 释放座位后触发 `LuaAutoSubstitute` 并推送 `substitute`
- [ ] `LeaveRoom` 释放座位后触发 `LuaAutoSubstitute` 并清理该用户队列位置
- [ ] `KickPlayerAndInterrupt` 后触发 `LuaAutoSubstitute`，Interrupted 补满后触发 `ResumeGame`
- [ ] 替补时队首余额不足跳过并继续下一位验证
- [ ] 机器人入队被 `LuaEnqueue` 拒绝验证
- [ ] 排队者断线重连后排队位置保留、`queue_list` 恢复展示验证

## RoomState 扩展（模块4 后端）
- [ ] `RoomState` DTO 新增 `QueueList []QueueInfo`（JSON tag `queue_list`）
- [ ] `BuildFullRoomState` 读取队列数据填充 `QueueList`

## 前端 gogain/packages/gift-box（模块4 前端）
- [ ] `types.ts` 新增 `QueueInfo` 类型；`RoomStateData` 新增 `queue_list: QueueInfo[]`
- [ ] `normalizeRoomState.ts` 对 `queue_list` 空数组兜底
- [ ] `homeEventNames.ts` 新增 `queueEnqueuePress`/`queueDequeuePress`
- [ ] `roomWsCommands.ts` 新增 `sendEnqueueWs`/`sendDequeueWs`
- [ ] `registerRoomWsCommandOrchestration` 订阅 `queueEnqueuePress`/`queueDequeuePress`
- [ ] `roomPushPayloads.ts` 新增 `parseSubstitutePayload` 与 `SubstitutePayload` 类型
- [ ] `RoomBottomAction.ts` 主按钮新增"预约排队"/"取消排队 N"两态，原有已入座两态不变
- [ ] `RoomScreen.ts` 排队状态判断（本地是否在 queue_list、房间是否满座）驱动按钮态
- [ ] `RoomSpectatorList.ts` 观众列表下方排队列表渲染（头像 + 位置），订阅 room.state 刷新
- [ ] `RoomScene.ts` 新增 `case 'substitute'`：本地用户被替补时触发 `ui.cmd.toast`
- [ ] i18n 三语（zh/en/es）新增 `room.enqueue`/`room.dequeue`/`room.queuePosition`/`room.substitutedToast`
- [ ] 纯观战未排队且房间未满座时不显示排队按钮验证
- [ ] "我要观战"复用现有离开座位流程、无新增按钮验证
- [ ] substitute 与 wait_replacement 互斥关系（有队列走自动替补、无队列走手动抢座）验证

## 协议文档
- [ ] `backend/docs/websocket_protocol.md` 新增 `enqueue`/`dequeue` 命令、`queue_list` 字段、`substitute` 推送
- [ ] `gogain/websocket_protocol.md` 同步上述变更
- [ ] `gogain/packages/gift-box/docs/room-ws-flow.md` 补充自动上座与排队替补流程图

## 机器人兼容性（模块5）
- [ ] `JoinRoom` 方法签名与逻辑未变
- [ ] 机器人走 `RobotPlayer.JoinAndReady` → `JoinRoom` 原路径不受影响验证
- [ ] 机器人不排队、调度器与排队队列协调（排队者优先、队列空时机器人补座）验证
