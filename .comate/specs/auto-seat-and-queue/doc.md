# 自动上座与排队替补机制

## 一、需求场景

### 现状
- 用户进入房间后**始终先成为观战者**，需手动执行 `select_seat` → `player_ready` 两步操作才能成为玩家
- 座位满后，新进入的观战者无法参与游戏，也没有排队替补机制
- 玩家退出后释放的座位无人自动补充
- 机器人（Robot）与真实用户共用同一套入房/选座/准备链路（`RobotPlayer.JoinAndReady` 内部依次调用 `JoinRoom` → 延迟 `SelectSeat` → 延迟 `PlayerReady`），依靠 2~5s / 1~3s 的随机延迟模拟真人节奏

### 目标
1. **座位未满时**：玩家进入房间自动分配座位并自动准备，同时可通过现有"离开座位"流程切换为观战状态
2. **座位已满时**：新进入的人自动为观战状态，观战者可选择"预约排队"，有人退出时按排队顺序自动替补上座
3. **机器人零改动**：通过调用路径分叉设计（新增 `JoinAndAutoSeat` 给真实玩家，`JoinRoom` 供机器人继续使用），机器人系统无需任何代码改动
4. **前端完整适配**：`gogain/packages/gift-box`（PIXI.js + TypeScript）新增排队 UI，两份协议文档（前后端）同步更新

## 二、模块功能拆解

### 模块1：入房自动上座

**功能描述**：新增 `JoinAndAutoSeat` 应用服务方法，专供真实玩家经 WS 入房时使用，将"加入房间+自动选座+自动准备"合并执行。机器人路径（`RobotPlayer.JoinAndReady` → `JoinRoom`）保持不变，无需任何适配。

**调用路径分叉设计**：
- 真实玩家：WS `join_room`/`auto_match` → `handleJoinRoom`/`handleAutoMatch` → **`JoinAndAutoSeat`**（新方法）
- 机器人：`RobotPlayer.JoinAndReady` → **`JoinRoom`**（原方法，不变）
- 两条路径在 gateway 层天然隔离：机器人不走 gateway/gRPC `Forward`，直接进程内调用 `JoinRoom`；真实玩家经 gateway 路由到 `handleJoinRoom`/`handleAutoMatch`，改为调用新方法

**技术方案**：
- **`JoinRoom` 保持不变**：继续承担"加入房间成为观战者"职责，机器人路径零影响
- **新增 `JoinAndAutoSeat` 方法**（`room_app_service.go`）：内部依次执行
  1. 调用现有 `JoinRoom` 逻辑（加入观战者集合）
  2. 检查房间状态：若 `Status == Playing`，直接返回纯观战状态（游戏进行中不上座）
  3. 余额校验：调用 `balanceService.CheckBalanceForReady`，余额不足则返回纯观战状态 + 余额不足提示
  4. 调用新 Lua 脚本 `LuaAutoSeatAndReady`：原子地查找最低编号空座位 + 选座 + 准备（spectator → player）
  5. 若无空座位 → 返回纯观战状态（满座自动观战，等价模块2）
  6. 若自动上座成功 → 返回 `IsSpectator=false` + 已入座已准备的房间状态
- **新增 Lua 脚本 `LuaAutoSeatAndReady`**：仅负责"选座+准备"的原子操作（不包含 join，join 由 `JoinRoom` 已完成）。复用现有 `LuaSelectSeat` + `LuaPlayerReady` 的核心逻辑，合并为单脚本避免两步间的并发竞争
- **`handleJoinRoom`/`handleAutoMatch` 改调用 `JoinAndAutoSeat`**：`generic_service.go` 中两个 handler 的 `roomAppSvc.JoinRoom` / `roomAppSvc.AutoMatchAndJoin` 调用点改为新方法。`AutoMatchAndJoin` 内部也改为调用 `JoinAndAutoSeat` 而非 `JoinRoom`
- **"我要观战"复用现有离开座位流程**：现有 `RoomSeatCornerButton`（右下角"离开座位/站起"）与 `RoomBottomAction` ready 态主按钮均已调用 `sendCancelSeatWs` → `cancel_seat`，后端 `LuaCancelSeat` 释放座位降级为观战者。无需新增按钮
- **前端无需特殊处理入房响应**：`gift-box` 的 `RoomSystem` 已通过 `room.evt.push` 的 `room_state` 类型驱动状态刷新，自动上座后 `findLocalSeat(state, userId)` 自然识别为已入座

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/application/room_app_service.go` | 修改 | 新增 `JoinAndAutoSeat` 方法；`AutoMatchAndJoin` 内部改调新方法 |
| `backend/game/server/generic_service.go` | 修改 | `handleJoinRoom`/`handleAutoMatch` 改调 `JoinAndAutoSeat` |
| `backend/game/infrastructure/persistence/redis/lua_scripts.go` | 新增 | `LuaAutoSeatAndReady` 原子脚本（选座+准备） |
| `backend/game/infrastructure/persistence/redis/repository.go` | 修改 | 新增 `AutoSeatAndReady` 仓储方法 |
| `backend/game/domain/repository.go` | 修改 | 新增 `AutoSeatAndReady` 接口定义 |

**边界条件**：
- 并发入房时多个用户争抢同一座位 → `LuaAutoSeatAndReady` 原子脚本保证安全
- 用户余额不足 → `JoinAndAutoSeat` 中余额校验失败，仅成为观战者
- 房间处于 Playing 状态 → 直接返回纯观战状态，不调用 `LuaAutoSeatAndReady`
- 机器人入房 → 走 `JoinRoom` 原路径，完全不受影响
- `AutoMatchAndJoin` 内部调用 `JoinAndAutoSeat`，自动继承自动上座逻辑

---

### 模块2：满座自动观战

**功能描述**：当房间座位已满（所有座位均被占有），新进入的用户自动成为纯观战者（seat_no=0），无需任何额外操作。

**技术方案**：
- `JoinAndAutoSeat` 方法在调用 `LuaAutoSeatAndReady` 时，若检测到无空座位，则跳过选座+准备，直接返回纯观战状态
- 满座判断逻辑：`LuaAutoSeatAndReady` 遍历座位 bitmap，若无空位返回"无空座"标记
- 现有 `LuaJoinAsSpectator`（由 `JoinRoom` 调用）已支持纯观战者加入，无需修改

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/application/room_app_service.go` | 修改 | `JoinAndAutoSeat` 中处理 `LuaAutoSeatAndReady` 返回的"无空座"分支 |
| `backend/game/infrastructure/persistence/redis/lua_scripts.go` | 修改 | `LuaAutoSeatAndReady` 中包含满座判断分支 |

**边界条件**：
- 所有座位被占用但部分已入座者未准备 → 仍视为满座
- 游戏进行中有玩家断线 → 断线玩家座位仍被占用，新入房者为观战

---

### 模块3：排队预约机制

**功能描述**：观战者可选择"预约排队"，系统维护一个排队队列。当有玩家/已入座者退出释放座位时，按队列顺序自动将排队中的观战者替补上座。

**技术方案**：
- 新增 Redis 有序集合 `cashparty:room:queue:{roomID}`，使用时间戳作为 score 实现先到先得排序
- 新增 Lua 脚本 `LuaEnqueue`：将观战者加入排队队列
- 新增 Lua 脚本 `LuaDequeue`：从队列中移除指定排队者（取消排队）
- 新增 Lua 脚本 `LuaAutoSubstitute`：在座位释放时（CancelSeat/LeaveRoom/KickPlayerAndInterrupt），原子地检测队列并自动替补
- 新增 WebSocket 命令 `enqueue`（预约排队）和 `dequeue`（取消排队）
- 修改 `CancelSeat`、`LeaveRoom`、`KickPlayerAndInterrupt` 逻辑，在释放座位后触发自动替补
- 排队者上座后通过推送通知前端更新状态
- **机器人排除**：`LuaEnqueue` 校验入队者 `is_robot` 字段，机器人不允许入队（机器人的设计目标是补座而非排队）。`LuaAutoSubstitute` 从队首取人时也再次校验，若队首为机器人则跳过并移除（防御性处理）
- **断线重连**：排队状态存储在 Redis 队列中，断线不影响排队位置；重连后通过 `room_state` 推送中的 `queue_list` 恢复前端展示
- **离开房间清理**：`LeaveRoom` 执行时若该用户在队列中，需同步从队列移除（`LuaDequeue` 或在 `LuaLeaveRoom` 中合并处理）

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/infrastructure/persistence/redis/lua_scripts.go` | 新增 | LuaEnqueue、LuaDequeue、LuaAutoSubstitute 脚本 |
| `backend/game/infrastructure/persistence/redis/repository.go` | 修改 | 新增队列相关仓储方法 |
| `backend/game/infrastructure/persistence/redis/keys.go` | 修改 | 新增排队队列 key 定义 |
| `backend/game/domain/repository.go` | 修改 | 新增队列操作接口定义 |
| `backend/game/application/seat_app_service.go` | 修改 | CancelSeat 后触发替补逻辑 |
| `backend/game/application/room_app_service.go` | 修改 | LeaveRoom 后触发替补逻辑；新增 Enqueue/Dequeue 方法；LeaveRoom 中清理排队状态 |
| `backend/game/application/game_app_service.go` | 修改 | KickPlayerAndInterrupt 后触发替补逻辑（与中断恢复流程协调） |
| `backend/game/server/generic_service.go` | 修改 | 新增 enqueue/dequeue 命令路由（Forward 方法 switch 新增 case） |
| `backend/config/gateway-router.yaml` | 修改 | 新增 enqueue/dequeue 命令到 game 服务的路由映射（路由表为配置驱动，非 router.go 硬编码） |
| `backend/common/message/types.go` | 修改 | 新增 `CmdEnqueue`/`CmdDequeue` 命令常量 |
| `backend/game/application/room_state.go` | 修改 | RoomState DTO 增加排队列表信息 |
| `backend/game/domain/events.go` | 修改 | 新增排队相关事件类型（QueueJoin/QueueLeave/Substitute） |
| `backend/game/domain/room.go` | 修改 | 新增排队者数据结构 |

**边界条件**：
- 多人同时排队 → Redis ZADD 原子操作保证顺序
- 排队者中途离开房间 → `LeaveRoom` 中同步从队列移除
- 排队者断线重连 → 排队位置保留，重连后通过 `room_state` 恢复展示
- 替补时排队者余额不足 → 跳过该排队者，通知余额不足，继续替补下一位
- 游戏进行中有人退出（KickPlayerAndInterrupt） → 房间进入中断状态（RoomStatusInterrupted）。此时 `LuaAutoSubstitute` 触发：若有排队者，直接替补上座并自动准备，当座位补满后由 `LuaPlayerReady` 的中断恢复分支触发 `ResumeGame`；若无排队者，座位保持空位，等待新入房者或机器人调度器补座
- 队列为空时释放座位 → 座位变为空位，等待新入房者自动上座或机器人调度器补座
- 机器人不允许入队 → `LuaEnqueue` 拒绝 `is_robot=true` 的用户

---

### 模块4：房间状态广播与前端适配

**功能描述**：扩展房间状态 DTO，新增排队列表信息；前端（`gogain/packages/gift-box`，PIXI.js + TypeScript）需根据新状态展示"预约排队/取消排队"按钮、排队位置等。"我要观战"无需新增，复用现有离开座位流程（见模块1）。

**前端工程定位**：
- 真实前端为 `e:\demo\party\gogain` monorepo（pnpm workspace + turbo），游戏核心在 `packages/gift-box`
- 技术栈：TypeScript + PIXI.js，事件总线驱动（`@redpacket/game-kit` 的 `TypedEventBus`），i18n 文案（非硬编码中文）
- 后端 `backend/test-ws.html` 仅为联调测试页，**非真实生产前端**，本次前端改动以 `gift-box` 为准
- 协议文档有两份需同步：`gogain/websocket_protocol.md`（前端侧）与 `backend/docs/websocket_protocol.md`（后端侧），保持一致

**技术方案**：
- 后端 `RoomState` DTO 新增 `QueueList []QueueInfo` 字段（JSON tag `queue_list`）
- 前端 `RoomStateData` 类型（`gogain/packages/gift-box/src/core/systems/room/types.ts`）新增 `queue_list: QueueInfo[]`
- `QueueInfo` 包含：`user_id`、`nickname`、`avatar`、`queue_position`、`queued_at`
- 排队者自动替补成功后推送 `substitute` 事件通知前端
- 前端根据用户当前状态（已入座未准备 / 已准备玩家 / 纯观战未排队 / 排队中）切换 `RoomBottomAction` 主按钮文案与行为

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/application/room_state.go` | 修改 | RoomState 新增 `QueueList`；`BuildFullRoomState` 读取队列数据填充 |
| `backend/game/domain/events.go` | 修改 | 新增排队事件（QueueJoin/QueueLeave）、替补事件（Substitute） |
| `backend/docs/websocket_protocol.md` | 修改 | 新增 `enqueue`/`dequeue` 命令说明；RoomState 新增 `queue_list` 字段；新增 `substitute` 推送类型 |
| `gogain/websocket_protocol.md` | 修改 | 同步上述协议变更（前端侧协议文档） |
| `gogain/packages/gift-box/docs/room-ws-flow.md` | 修改 | 补充自动上座与排队替补流程图 |
| `gogain/packages/gift-box/src/core/systems/room/types.ts` | 修改 | `RoomStateData` 新增 `queue_list` 字段；新增 `QueueInfo` 类型 |
| `gogain/packages/gift-box/src/core/systems/room/normalizeRoomState.ts` | 修改 | 归一化处理 `queue_list`（空数组兜底） |
| `gogain/packages/gift-box/src/core/room/roomWsCommands.ts` | 修改 | 新增 `sendEnqueueWs`/`sendDequeueWs`；`registerRoomWsCommandOrchestration` 订阅排队相关 `HOME_INTERACTION` 事件 |
| `gogain/packages/gift-box/src/core/room/roomPushPayloads.ts` | 修改 | 新增 `substitute` 推送解析器与 payload 类型 |
| `gogain/packages/gift-box/src/events/homeEventNames.ts` | 修改 | `HOME_INTERACTION` 新增 `queueEnqueuePress`/`queueDequeuePress` |
| `gogain/packages/gift-box/src/room/RoomScreen.ts` | 修改 | 新增排队状态判断；扩展 `onPressAction`/按钮切换逻辑 |
| `gogain/packages/gift-box/src/room/widgets/room-bottom-action/RoomBottomAction.ts` | 修改 | 新增排队态主按钮文案与显隐规则 |
| `gogain/packages/gift-box/src/room/widgets/room-spectator-list/RoomSpectatorList.ts` | 修改 | 下方新增排队列表渲染区域 |
| `gogain/packages/gift-box/src/scene/RoomScene.ts` | 修改 | 订阅 `substitute` 推送；处理自动上座入房响应 |
| `gogain/packages/gift-box/src/core/i18n/locales/zh.ts` & `en.ts` & `es.ts` | 修改 | 新增排队相关 i18n key |

**前端详细改动（`gogain/packages/gift-box`）**：

当前前端基于 PIXI.js + TypeScript，事件总线驱动。座位/观战相关核心逻辑分布在 `RoomScreen.ts`、`RoomBottomAction.ts`、`RoomCentralTable.ts`、`spectatorSnatchPolicy.ts`。"我要观战"已由现有离开座位流程覆盖，本次仅新增排队相关能力。需做以下改动：

1. **类型与状态扩展（`types.ts` + `normalizeRoomState.ts`）**：
   - `RoomStateData` 新增 `queue_list: QueueInfo[]`
   - 新增 `QueueInfo` 类型：`{ user_id, nickname, avatar, queue_position, queued_at }`
   - `normalizeRoomStateData` 中对 `queue_list` 做空数组兜底（`state.queue_list ?? []`）

2. **新增 WS 命令发送函数（`roomWsCommands.ts`）**：
   - 新增 `sendEnqueueWs(deps)`：发送 `enqueue` 命令
   - 新增 `sendDequeueWs(deps)`：发送 `dequeue` 命令
   - `registerRoomWsCommandOrchestration` 中新增事件订阅：
     - `HOME_INTERACTION.queueEnqueuePress` → `sendEnqueueWs`
     - `HOME_INTERACTION.queueDequeuePress` → `sendDequeueWs`
   - 错误码 `1003`（余额不足）已有处理，复用即可

3. **新增"预约排队"/"取消排队"按钮（`RoomBottomAction.ts`）**：
   - 新增 i18n key：`room.enqueue`（预约排队）、`room.dequeue`（取消排队）、`room.queuePosition`（排队位置 N）
   - 按钮状态切换规则（扩展 `onPressAction` 逻辑，原有已入座两态不变）：
     - 已入座未准备 → 主按钮"准备"（`room.ready`），副按钮/角标"离开座位"（现有，不变）
     - 已准备玩家 → 主按钮"离开"（`room.leave`，现有，不变），游戏中隐藏
     - 纯观战未排队 且 房间满座 → 主按钮"预约排队"（`room.enqueue`）
     - 排队中 → 主按钮"取消排队 N"（`room.dequeue` + 排队位置）
   - 纯观战未排队 且 房间未满座 → 不显示排队按钮（用户可直接点空座位上座，或后端自动上座已处理）

4. **排队列表渲染（`RoomSpectatorList.ts`）**：
   - 在现有观众列表（最多 3 个头像 + 计数）下方，新增排队列表渲染区域
   - 新增 `renderQueueList(queueList)` 逻辑：显示排队者头像 + 排队位置编号
   - 复用 `filterSpectatorsNotInSeats` 思路，避免排队者与已替补上座者重复显示
   - 订阅 `room.state` observable，`queue_list` 变化时刷新

5. **入房响应处理调整（`RoomScene.ts` + `roomWsCommands.ts`）**：
   - 现有 `sendJoinRoomWs`/`sendAutoMatchWs` 的响应中 `is_spectator` 现在可能为 `false`（自动上座成功）
   - `RoomScene` 在收到入房响应后，需依据 `room_state` 推送（而非仅响应字段）刷新本地状态——当前已通过 `room.evt.push` 的 `room_state` 类型驱动，**无需特殊处理**，只需确保后端在自动上座成功后立即广播 `room_state`
   - 自动上座后用户本地状态由 `findLocalSeat(state, userId)` 计算得出（已入座），`isLocalSpectator()` 返回 false，按钮状态自然切换为"已准备玩家"

6. **新增 `substitute` 推送处理（`RoomScene.ts` + `roomPushPayloads.ts`）**：
   - `roomPushPayloads.ts` 新增 `parseSubstitutePayload` → `SubstitutePayload { user_id, seat_no, room_state }`
   - `RoomScene.ts` 在 `room.evt.push` 订阅中新增 `case 'substitute'`：
     - 若 `user_id` 为本地用户 → 触发"已自动替补上座"提示（`ui.cmd.toast`），状态由随后的 `room_state` 推送自然刷新
     - 其他用户的替补 → 由 `room_state` 推送统一渲染
   - 与现有 `wait_replacement`（观战抢座）流程的关系：
     - `wait_replacement` 是当前游戏进行中（status=2/4）释放座位时给观战者的**手动抢座**提示，本次保留不变
     - `substitute` 是排队队列中的**自动替补**通知，二者场景互斥（有排队队列走自动替补，无排队队列走 `wait_replacement` 手动抢座）

7. **离开座位后的 UI 引导**：
   - 现有离开座位（`cancelSeat`）成功后，用户变为纯观战者。若房间仍满座，`RoomBottomAction` 应自动切换主按钮为"预约排队"

8. **i18n 文案新增（`zh.ts` / `en.ts` / `es.ts`）**：
   - `room.enqueue`: "预约排队" / "Queue" / "Hacer cola"
   - `room.dequeue`: "取消排队" / "Cancel queue" / "Cancelar cola"
   - `room.queuePosition`: "排队中 #{n}" / "Queued #{n}" / "En cola #{n}"
   - `room.substitutedToast`: "已自动替补上座" / "Auto-substituted into seat" / "Sustituido automáticamente"

---

### 模块5：机器人系统兼容性说明

**功能描述**：由于采用调用路径分叉设计（模块1），机器人路径（`RobotPlayer.JoinAndReady` → `JoinRoom`）完全不经过 `JoinAndAutoSeat`，机器人系统无需任何代码改动。本模块仅说明机器人与排队机制的协调关系。

**机器人零改动依据**：
- `JoinRoom` 方法签名与逻辑保持不变 → `RobotPlayer.JoinAndReady`（`robot_player.go:72`）无感知
- 机器人不走 gateway/gRPC `Forward`，直接进程内调用 `roomAppSvc.JoinRoom` → 不受 `handleJoinRoom`/`handleAutoMatch` 改调 `JoinAndAutoSeat` 影响
- 机器人延迟选座/准备链路（`ScheduleAction("seat")` → `ScheduleAction("ready")`）不变 → 不存在 `AlreadySeated`/`AlreadyReady` 问题
- `RobotBehaviorEngine`、`RobotSchedulerService`、`RobotAccountService` 均无需修改

**机器人与排队机制的协调**（无需改代码，仅运行时行为说明）：
- **机器人不参与排队**：`LuaEnqueue` 校验 `is_robot` 字段，拒绝机器人入队（模块3已覆盖）。`RobotSchedulerService` 不调用 `Enqueue`，机器人仅通过 `JoinAndReady` → `SelectSeat` 直接抢空座
- **排队者优先于机器人补座**：当座位释放且排队队列非空时，`LuaAutoSubstitute` 由排队者自动替补，机器人调度器不介入。当排队队列为空时，座位保持空位，`RobotSchedulerService.scanRooms` 在下一轮扫描中发现该 Waiting 房间空座数增加，按现有逻辑分配机器人补座
- **`SeatedCount` 计算不受影响**：排队中的观战者未入座，不计入 `scanRooms` 的 `SeatedCount`，机器人调度器的补座计算天然正确
- **中断状态协调**：`KickPlayerAndInterrupt` 后 `LuaAutoSubstitute` 触发排队者替补并自动准备，补满后触发 `ResumeGame`；若无排队者，等待机器人调度器补座或真人入房

**涉及文件**：无（机器人系统零改动）

**边界条件**：
- 机器人调度器扫描到满座房间 → 不分配机器人（现有逻辑，无需修改）
- 排队队列非空时释放座位 → 排队者优先替补，机器人不介入
- 排队队列为空时释放座位 → 机器人调度器下一轮扫描补座

---

## 三、数据流路径

### 自动上座流程（真实玩家）
```
gogain gift-box → HOME_INTERACTION.quickStartPress / channelTap
  → sendAutoMatchWs / sendJoinRoomWs → WebSocket → Gateway → gRPC
  → handleJoinRoom / handleAutoMatch → JoinAndAutoSeat (新方法):
      1. 调用 JoinRoom (原方法，加入观战者)
      2. if Status == Playing → 返回纯观战状态
      3. 余额校验 (CheckBalanceForReady) → 不足则返回纯观战状态 + 提示
      4. LuaAutoSeatAndReady (新脚本，原子选座+准备):
           if 空座位存在 → 分配最低编号空座位 + 自动准备 → 返回已入座已准备
           else → 返回"无空座" → 纯观战状态
  → 广播房间状态（含 queue_list）
  → gift-box RoomSystem 收到 room_state 推送 → findLocalSeat 自然识别为已入座
```

### 机器人入房流程（完全不变）
```
RobotScheduler → RobotPlayer.JoinAndReady
  → JoinRoom (原方法，不变) → LuaJoinAsSpectator → 加入观战者(无座位)
  → ScheduleAction("seat", 2~5s) → SelectSeat(随机空座) → ScheduleAction("ready", 1~3s) → PlayerReady
```

### 我要观战流程（复用现有离开座位）
```
gogain gift-box → RoomSeatCornerButton "离开座位" (未准备态) / RoomBottomAction "离开" (已准备态)
  → emit HOME_INTERACTION.roomStandPress → sendCancelSeatWs → WebSocket(cancel_seat)
  → Gateway → gRPC → SeatService.CancelSeat
  → LuaCancelSeat (复用现有):
      释放座位 → 检查排队队列 → 触发自动替补 (LuaAutoSubstitute)
  → 广播房间状态（含 queue_list）
  → gift-box RoomScreen 检测到 isLocalSpectator=true 且满座 → 主按钮切换为"预约排队"
```

### 排队替补流程
```
gogain gift-box → RoomBottomAction "预约排队" 按钮
  → emit HOME_INTERACTION.queueEnqueuePress → sendEnqueueWs → WebSocket(enqueue)
  → Gateway → gRPC → RoomService.Enqueue
  → LuaEnqueue:
      校验非机器人 → 加入排队有序集合 → 返回排队位置
  → 广播房间状态（含 queue_list）
  → gift-box RoomScreen 检测到自己在 queue_list 中 → 主按钮切换为"取消排队 N"

座位释放时（CancelSeat/LeaveRoom/KickPlayerAndInterrupt）:
  → LuaAutoSubstitute:
      if 排队队列非空:
          取出队首排队者（跳过机器人）→ 分配释放的座位 → 自动准备 → 从队列移除
          → 推送 substitute 事件给替补者
          → 广播房间状态（含 queue_list）
          → gift-box RoomScene 收到 substitute 推送 → ui.cmd.toast 提示"已自动替补上座"
          → 若房间处于 Interrupted 且座位补满 → 触发 ResumeGame
      else:
          座位保持空位（等待新入房者或机器人调度器补座）
          → 若游戏进行中释放座位 → 触发现有 wait_replacement 观战抢座流程
```

## 四、人天计划

| 模块 | 功能 | 人天 |
|------|------|------|
| **模块1** | 入房自动上座 | 1.5人天 |
| | 1.1 设计并实现 `LuaAutoSeatAndReady` 原子脚本（选座+准备） | 0.5人天 |
| | 1.2 新增 `JoinAndAutoSeat` 方法 + `AutoMatchAndJoin` 改调 + `handleJoinRoom`/`handleAutoMatch` 改调（含余额校验） | 0.5人天 |
| | 1.3 前端 RoomScene 自动上座状态适配（依赖 room_state 推送刷新，无需新增按钮） | 0.5人天 |
| **模块2** | 满座自动观战 | 0.25人天 |
| | 2.1 `JoinAndAutoSeat` 中处理 `LuaAutoSeatAndReady` 返回的"无空座"分支（含余额不足降级） | 0.25人天 |
| **模块3** | 排队预约机制 | 3.5人天 |
| | 3.1 Redis 队列数据结构设计与 LuaEnqueue/LuaDequeue 脚本 | 0.5人天 |
| | 3.2 LuaAutoSubstitute 原子替补脚本（含机器人排除） | 1人天 |
| | 3.3 CancelSeat/LeaveRoom/KickPlayerAndInterrupt 触发替补逻辑 | 0.75人天 |
| | 3.4 Enqueue/Dequeue 应用服务与命令路由（generic_service + gateway-router.yaml + types.go） | 0.5人天 |
| | 3.5 断线重连、离开房间时队列清理 | 0.5人天 |
| | 3.6 替补时余额不足的异常处理 | 0.25人天 |
| **模块4** | 房间状态与前端适配（gogain/packages/gift-box） | 3人天 |
| | 4.1 后端 RoomState DTO 扩展 queue_list + QueueInfo | 0.25人天 |
| | 4.2 排队/替补事件定义与推送（QueueJoin/QueueLeave/Substitute） | 0.5人天 |
| | 4.3 前端类型扩展（types.ts + normalizeRoomState.ts） | 0.25人天 |
| | 4.4 前端 WS 命令扩展（roomWsCommands.ts：sendEnqueueWs/sendDequeueWs + 事件订阅） | 0.5人天 |
| | 4.5 前端 UI 适配（RoomBottomAction 排队态主按钮、RoomSpectatorList 排队列表） | 1人天 |
| | 4.6 前端 substitute 推送处理与 i18n 文案（zh/en/es） | 0.5人天 |
| **模块5** | 机器人系统兼容性（零改动，仅协调说明） | 0人天 |
| **协议文档** | 前后端两份 websocket_protocol.md + room-ws-flow.md 同步更新 | 0.75人天 |
| **联调测试** | 端到端集成测试 | 1.5人天 |
| | 7.1 并发场景测试（多人同时入房、排队、替补） | 0.5人天 |
| | 7.2 边界条件测试（断线、余额不足、中断状态） | 0.5人天 |
| | 7.3 机器人兼容性测试（机器人走原路径不受影响、不排队、调度器与队列协调） | 0.5人天 |
| **合计** | | **10.5人天** |

## 五、预期产出

1. 用户入房体验优化：空座时自动上座并准备，无需手动选座和准备操作
2. 灵活的角色切换：已入座用户可通过现有"离开座位"流程随时切换为观战
3. 排队替补机制：满座后观战者可排队，退出时自动替补
4. 机器人零改动：调用路径分叉设计使机器人系统完全不受影响，调度器与排队机制运行时自然协调
5. 前端完整适配：`gogain/packages/gift-box` 新增"预约排队"/"取消排队"按钮、排队列表展示、替补通知，与现有 `wait_replacement` 流程互斥协调，前后端两份协议文档同步更新
6. 完善的异常处理：余额不足、断线重连、中断状态等场景的健壮处理

## 六、风险与注意事项

1. **`JoinAndAutoSeat` 与 `JoinRoom` 的两步原子性**：新方案将"加入观战"（`JoinRoom`）与"选座+准备"（`LuaAutoSeatAndReady`）拆为两步，两步之间可能被其他用户抢座。但 `LuaAutoSeatAndReady` 内部原子地查找空座+选座+准备，若此时无空座则返回纯观战状态，用户可继续排队。该窗口期行为可接受，无需追求 join+seat 的单原子操作
2. **余额校验的性能影响**：`JoinAndAutoSeat` 每次入房都触发 `CheckBalanceForReady`，增加一次平台 RPC。需评估高并发下的延迟与超时
3. **中断状态替补与 ResumeGame 的时序**：`KickPlayerAndInterrupt` 触发 `LuaAutoSubstitute` 替补后，若座位补满需触发 `ResumeGame`。需确保替补原子的"上座+准备"与 `LuaPlayerReady` 的中断恢复分支正确衔接，避免重复触发或竞态
4. **排队队列 TTL**：`cashparty:room:queue:{roomID}` 需与房间 hash 同步设置 24h TTL，房间销毁时队列应自动过期
5. **前端按钮状态机复杂度**：`RoomBottomAction` 多态按钮切换需覆盖"入房自动上座→离开座位→排队→替补上座→游戏开始"全链路，且需与现有 `wait_replacement` 观战抢座流程互斥（有队列走自动替补，无队列走手动抢座），需充分测试 UI 状态一致性
6. **前端架构契合度**：`gift-box` 采用事件总线 + i18n 驱动，新增按钮须通过 `HOME_INTERACTION.*` 事件编排，不可在 widget 内直接调用 WS 命令；i18n 文案需三语（zh/en/es）同步，避免漏译
