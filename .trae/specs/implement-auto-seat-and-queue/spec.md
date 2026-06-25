# 自动上座与排队替补机制 (Auto Seat & Queue Substitute) Spec

## Why

当前用户进入房间后**始终先成为观战者**，需手动执行 `select_seat` → `player_ready` 两步才能成为玩家，操作链路冗长；座位满后新进入的观战者既无法参与游戏，也无排队替补机制，玩家退出后释放的座位无人自动补充，影响上座率与游戏体验。

核心目标：
1. 座位未满时，真实玩家入房自动上座并自动准备
2. 座位已满时，新进入的人自动为观战者，观战者可"预约排队"，有人退出时按队列顺序自动替补上座
3. 机器人系统零改动：通过调用路径分叉设计，机器人继续走原 `JoinRoom` 路径，不受自动上座影响
4. 前端 `gogain/packages/gift-box`（PIXI.js + TypeScript）完整适配排队 UI，前后端两份协议文档同步更新

## What Changes

### 新增能力
- **入房自动上座（真实玩家）**：新增 `JoinAndAutoSeat` 应用服务方法 + `LuaAutoSeatAndReady` 原子脚本，将"加入房间+自动选座+自动准备"合并执行
- **满座自动观战**：`LuaAutoSeatAndReady` 检测无空座位时返回纯观战状态
- **排队预约机制**：新增 Redis 有序集合队列 + `LuaEnqueue`/`LuaDequeue`/`LuaAutoSubstitute` 脚本，支持观战者排队与自动替补
- **新 WS 命令**：`enqueue`（预约排队）、`dequeue`（取消排队）
- **新推送类型**：`substitute`（替补上座通知）
- **RoomState 扩展**：新增 `queue_list` 字段广播排队列表
- **前端排队 UI**：`RoomBottomAction` 新增"预约排队/取消排队"主按钮态、`RoomSpectatorList` 新增排队列表渲染、`RoomScene` 处理 `substitute` 推送

### 修改点
- `handleJoinRoom`/`handleAutoMatch`/`AutoMatchAndJoin` 改调 `JoinAndAutoSeat`（原 `JoinRoom` 保持不变供机器人使用）
- `CancelSeat`/`LeaveRoom`/`KickPlayerAndInterrupt` 释放座位后触发 `LuaAutoSubstitute`
- `gateway-router.yaml` + `common/message/types.go` 新增 `enqueue`/`dequeue` 路由与常量
- 前端 `types.ts`/`normalizeRoomState.ts`/`roomWsCommands.ts`/`roomPushPayloads.ts`/`homeEventNames.ts`/`RoomScreen.ts`/`RoomBottomAction.ts`/`RoomSpectatorList.ts`/`RoomScene.ts`/i18n 三语扩展

### 非变更项（明确边界）
- `JoinRoom` 方法签名与逻辑保持不变，机器人路径（`RobotPlayer.JoinAndReady` → `JoinRoom`）零改动
- "我要观战"复用现有"离开座位"流程（`RoomSeatCornerButton` / `RoomBottomAction` ready 态主按钮 → `sendCancelSeatWs`），无需新增按钮
- 现有 `wait_replacement` 观战抢座流程保留不变，与新增 `substitute` 自动替补互斥（有排队队列走自动替补，无队列走手动抢座）

## Impact
- **Affected specs**: 机器人系统（`implement-robot-system`）— 运行时协调，无代码依赖
- **Affected code（后端）**:
  - `backend/game/application/room_app_service.go` — 新增 `JoinAndAutoSeat`/`Enqueue`/`Dequeue` 方法
  - `backend/game/application/seat_app_service.go` — `CancelSeat` 触发替补
  - `backend/game/application/game_app_service.go` — `KickPlayerAndInterrupt` 触发替补
  - `backend/game/application/room_state.go` — `RoomState` 新增 `QueueList`
  - `backend/game/server/generic_service.go` — 入房改调 + `enqueue`/`dequeue` 命令路由
  - `backend/game/infrastructure/persistence/redis/lua_scripts.go` — 4 个新 Lua 脚本
  - `backend/game/infrastructure/persistence/redis/repository.go` + `keys.go` — 队列仓储
  - `backend/game/domain/repository.go` + `events.go` + `room.go` — 接口/事件/结构
  - `backend/config/gateway-router.yaml` + `backend/common/message/types.go` — 路由与常量
  - `backend/docs/websocket_protocol.md` — 协议文档
- **Affected code（前端 gogain/packages/gift-box）**:
  - `src/core/systems/room/types.ts` + `normalizeRoomState.ts`
  - `src/core/room/roomWsCommands.ts` + `roomPushPayloads.ts`
  - `src/events/homeEventNames.ts`
  - `src/room/RoomScreen.ts` + `widgets/room-bottom-action/RoomBottomAction.ts` + `widgets/room-spectator-list/RoomSpectatorList.ts`
  - `src/scene/RoomScene.ts`
  - `src/core/i18n/locales/zh.ts` + `en.ts` + `es.ts`
  - `docs/room-ws-flow.md`
- **Affected docs**: `gogain/websocket_protocol.md`

## ADDED Requirements

### Requirement: 入房自动上座（真实玩家）
系统 SHALL 在真实玩家经 WS `join_room`/`auto_match` 入房时，调用 `JoinAndAutoSeat` 方法：先执行 `JoinRoom` 加入观战者集合，再校验房间状态与余额，最后通过 `LuaAutoSeatAndReady` 原子脚本分配最低编号空座位并自动准备。

#### Scenario: 房间有空座且余额充足
- **WHEN** 真实玩家入房且房间 `Status != Playing` 且有空座位且余额充足
- **THEN** 玩家自动分配最低编号空座位并自动准备，返回 `IsSpectator=false`，广播 `room_state`

#### Scenario: 房间满座
- **WHEN** 真实玩家入房且所有座位被占用
- **THEN** `LuaAutoSeatAndReady` 返回"无空座"，玩家成为纯观战者（seat_no=0），返回 `IsSpectator=true`

#### Scenario: 游戏进行中入房
- **WHEN** 真实玩家入房且房间 `Status == Playing`
- **THEN** 跳过自动上座，玩家成为纯观战者

#### Scenario: 余额不足
- **WHEN** 真实玩家入房且 `CheckBalanceForReady` 校验失败
- **THEN** 跳过自动上座，玩家成为纯观战者并收到余额不足提示

#### Scenario: 并发入房争抢座位
- **WHEN** 多个玩家同时入房且空座位数 < 入房人数
- **THEN** `LuaAutoSeatAndReady` 原子脚本保证仅与空座位数等量的玩家上座成功，其余成为纯观战者

### Requirement: 调用路径分叉（机器人零改动）
系统 SHALL 保持 `JoinRoom` 方法签名与逻辑不变，机器人路径（`RobotPlayer.JoinAndReady` → `JoinRoom`）完全不经 `JoinAndAutoSeat`，机器人继续走延迟选座/准备节奏。

#### Scenario: 机器人入房
- **WHEN** 机器人调度器调用 `RobotPlayer.JoinAndReady`
- **THEN** 机器人经 `JoinRoom` 成为观战者，随后按 2~5s/1~3s 延迟执行 `SelectSeat`/`PlayerReady`，不受自动上座影响

### Requirement: 排队预约
系统 SHALL 提供基于 Redis 有序集合 `cashparty:room:queue:{roomID}` 的排队队列（时间戳 score 实现 FIFO），通过 `enqueue`/`dequeue` WS 命令支持观战者排队与取消排队，机器人不允许入队。

#### Scenario: 观战者预约排队
- **WHEN** 纯观战者发送 `enqueue` 命令
- **THEN** `LuaEnqueue` 校验非机器人后将其加入队列，返回排队位置，广播含 `queue_list` 的 `room_state`

#### Scenario: 观战者取消排队
- **WHEN** 排队中用户发送 `dequeue` 命令
- **THEN** `LuaDequeue` 将其从队列移除，广播更新后的 `room_state`

#### Scenario: 机器人尝试排队
- **WHEN** `is_robot=true` 的用户请求 `enqueue`
- **THEN** `LuaEnqueue` 拒绝入队并返回错误

#### Scenario: 排队者离开房间
- **WHEN** 排队中用户执行 `LeaveRoom`
- **THEN** 同步从队列移除该用户，广播更新后的 `room_state`

#### Scenario: 排队者断线重连
- **WHEN** 排队中用户断线后重连
- **THEN** 排队位置保留，重连后通过 `room_state` 的 `queue_list` 恢复前端展示

### Requirement: 自动替补
系统 SHALL 在座位释放时（`CancelSeat`/`LeaveRoom`/`KickPlayerAndInterrupt`）通过 `LuaAutoSubstitute` 原子脚本从排队队列队首取出排队者（跳过机器人）分配释放的座位并自动准备，向替补者推送 `substitute` 事件。

#### Scenario: 有排队者时释放座位
- **WHEN** 玩家释放座位且排队队列非空
- **THEN** 队首排队者被分配该座位并自动准备，从队列移除，向其推送 `substitute`，广播 `room_state`

#### Scenario: 无排队者时释放座位
- **WHEN** 玩家释放座位且排队队列为空
- **THEN** 座位保持空位；若游戏进行中释放，触发现有 `wait_replacement` 观战抢座流程

#### Scenario: 替补者余额不足
- **WHEN** 队首排队者余额不足
- **THEN** 跳过该排队者并通知余额不足，继续替补下一位

#### Scenario: 中断状态替补后恢复游戏
- **WHEN** 房间处于 Interrupted 状态时释放座位且排队者替补后座位补满
- **THEN** 触发 `ResumeGame`

### Requirement: 前端排队 UI（gogain/packages/gift-box）
前端 SHALL 在 `RoomBottomAction` 主按钮新增"预约排队/取消排队"两态，在 `RoomSpectatorList` 新增排队列表渲染，在 `RoomScene` 处理 `substitute` 推送，所有新增按钮通过 `HOME_INTERACTION.*` 事件编排，i18n 三语同步。

#### Scenario: 纯观战者满座时看到预约排队按钮
- **WHEN** 本地用户为纯观战者且未排队且房间满座
- **THEN** `RoomBottomAction` 主按钮显示"预约排队"（`room.enqueue`），点击 emit `HOME_INTERACTION.queueEnqueuePress`

#### Scenario: 排队中看到取消排队按钮
- **WHEN** 本地用户在 `queue_list` 中
- **THEN** 主按钮显示"取消排队 N"（`room.dequeue` + 排队位置），点击 emit `HOME_INTERACTION.queueDequeuePress`

#### Scenario: 被替补上座收到提示
- **WHEN** 本地用户收到 `substitute` 推送且 `user_id` 为本地用户
- **THEN** `RoomScene` 触发 `ui.cmd.toast` 提示"已自动替补上座"，状态由随后的 `room_state` 推送自然刷新

#### Scenario: 排队列表展示
- **WHEN** `room.state` 的 `queue_list` 变化
- **THEN** `RoomSpectatorList` 在观众列表下方渲染排队者头像与排队位置

## MODIFIED Requirements

### Requirement: 入房响应处理
`handleJoinRoom`/`handleAutoMatch`/`AutoMatchAndJoin` SHALL 改调 `JoinAndAutoSeat` 而非 `JoinRoom`/`AutoMatchAndJoin` 原逻辑，入房响应中 `is_spectator` 可能为 `false`（自动上座成功），前端依赖 `room_state` 推送刷新本地状态而非仅依赖响应字段。

### Requirement: 房间状态广播
`RoomState` DTO SHALL 新增 `queue_list []QueueInfo` 字段（JSON tag `queue_list`），`BuildFullRoomState` 读取队列数据填充。`QueueInfo` 包含 `user_id`、`nickname`、`avatar`、`queue_position`、`queued_at`。

### Requirement: 前端 RoomStateData 类型
前端 `RoomStateData`（`types.ts`）SHALL 新增 `queue_list: QueueInfo[]` 字段，`normalizeRoomStateData` 对其做空数组兜底。

## REMOVED Requirements
无
