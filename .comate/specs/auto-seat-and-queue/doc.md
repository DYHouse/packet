# 自动上座与排队替补机制

## 一、需求场景

### 现状
- 用户进入房间后**始终先成为观战者**，需手动执行 `select_seat` → `player_ready` 两步操作才能成为玩家
- 座位满后，新进入的观战者无法参与游戏，也没有排队替补机制
- 玩家退出后释放的座位无人自动补充

### 目标
1. **座位未满时**：玩家进入房间自动分配座位（自动显示在座位上），同时可点击"我要观战"切换为观战状态
2. **座位已满时**：新进入的人自动为观战状态，观战者可选择"预约排队"，有人退出时按排队顺序自动替补上座

## 二、模块功能拆解

### 模块1：入房自动上座

**功能描述**：修改 `JoinRoom` / `AutoMatchAndJoin` 逻辑，当房间有空座位时，用户入房后自动分配一个空座位，状态为"已入座观战者"（有座位但未准备），同时保留手动切换为纯观战的入口。

**技术方案**：
- 修改 `JoinRoom` 应用服务方法，在执行 `LuaJoinAsSpectator` 后，判断是否有空座位
- 若有空座位，自动调用 `SelectSeat` 逻辑分配最低编号的空座位
- 新增 Lua 脚本 `LuaJoinAndAutoSeat`，将"加入房间+自动选座"合并为原子操作，避免并发问题
- 新增 WebSocket 命令 `switch_to_spectator`（我要观战），允许已入座用户放弃座位变为纯观战者
- 前端需在入座状态下展示"我要观战"按钮

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/application/room_app_service.go` | 修改 | JoinRoom 逻辑增加自动选座分支 |
| `backend/game/infrastructure/persistence/redis/lua_scripts.go` | 新增 | LuaJoinAndAutoSeat 原子脚本 |
| `backend/game/infrastructure/persistence/redis/repository.go` | 修改 | 新增 JoinAndAutoSeat 仓储方法 |
| `backend/game/domain/repository.go` | 修改 | 新增 JoinAndAutoSeat 接口定义 |
| `backend/game/server/generic_service.go` | 修改 | 新增 switch_to_spectator 命令路由 |
| `backend/game/application/seat_app_service.go` | 修改 | 新增 SwitchToSpectator 方法 |
| `backend/gateway/router/router.go` | 修改 | 新增 switch_to_spectator 命令映射 |

**边界条件**：
- 并发入房时多个用户争抢同一座位 → Lua 原子脚本保证安全
- 用户余额不足以参与该房间 → 自动选座前需检查余额，余额不足则仅成为观战者
- 房间处于 Playing 状态时入房 → 仍为观战者，不上座

---

### 模块2：满座自动观战

**功能描述**：当房间座位已满（所有座位均被占有），新进入的用户自动成为纯观战者（seat_no=0），无需任何额外操作。

**技术方案**：
- 修改 `JoinRoom` 逻辑：在自动选座分支中，若检测到无空座位，则仅执行原 `LuaJoinAsSpectator` 逻辑
- 满座判断逻辑：遍历座位 bitmap，检查是否所有座位均被占用
- 现有 `LuaJoinAsSpectator` 已支持纯观战者加入，无需修改脚本本身

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/application/room_app_service.go` | 修改 | JoinRoom 中增加满座判断分支 |
| `backend/game/infrastructure/persistence/redis/lua_scripts.go` | 修改 | LuaJoinAndAutoSeat 中包含满座分支 |

**边界条件**：
- 所有座位被占用但部分已入座者未准备 → 仍视为满座
- 游戏进行中有玩家断线 → 断线玩家座位仍被占用，新入房者为观战

---

### 模块3：排队预约机制

**功能描述**：观战者可选择"预约排队"，系统维护一个排队队列。当有玩家/已入座者退出释放座位时，按队列顺序自动将排队中的观战者替补上座。

**技术方案**：
- 新增 Redis 有序集合 `cashparty:room:queue:{roomID}`，使用时间戳作为 score 实现先到先得排序
- 新增 Lua 脚本 `LuaEnqueue`：将观战者加入排队队列
- 新增 Lua 脚本 `LuaDequeue`：从队列头部取出下一个排队者
- 新增 Lua 脚本 `LuaAutoSubstitute`：在座位释放时（CancelSeat/LeaveRoom），原子地检测队列并自动替补
- 新增 WebSocket 命令 `enqueue`（预约排队）和 `dequeue`（取消排队）
- 修改 `CancelSeat`、`LeaveRoom` 逻辑，在释放座位后触发自动替补
- 排队者上座后通过推送通知前端更新状态

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/infrastructure/persistence/redis/lua_scripts.go` | 新增 | LuaEnqueue、LuaDequeue、LuaAutoSubstitute 脚本 |
| `backend/game/infrastructure/persistence/redis/repository.go` | 修改 | 新增队列相关仓储方法 |
| `backend/game/domain/repository.go` | 修改 | 新增队列操作接口定义 |
| `backend/game/application/seat_app_service.go` | 修改 | CancelSeat 后触发替补逻辑 |
| `backend/game/application/room_app_service.go` | 修改 | LeaveRoom 后触发替补逻辑；新增 Enqueue/Dequeue 方法 |
| `backend/game/server/generic_service.go` | 修改 | 新增 enqueue/dequeue 命令路由 |
| `backend/gateway/router/router.go` | 修改 | 新增 enqueue/dequeue 命令映射 |
| `backend/game/application/room_state.go` | 修改 | RoomState DTO 增加排队列表信息 |
| `backend/game/domain/events.go` | 修改 | 新增排队相关事件类型 |
| `backend/game/domain/room.go` | 修改 | 新增排队者数据结构 |

**边界条件**：
- 多人同时排队 → Redis ZADD 原子操作保证顺序
- 排队者中途离开房间 → 需从队列中移除
- 排队者断线重连 → 需恢复排队状态
- 替补时排队者余额不足 → 跳过该排队者，通知余额不足，继续替补下一位
- 游戏进行中有人退出 → 中断状态（RoomStatusInterrupted），此时替补逻辑与中断恢复逻辑需协调
- 队列为空时释放座位 → 座位变为空位，等待新入房者自动上座

---

### 模块4：房间状态广播与前端适配

**功能描述**：扩展房间状态 DTO，新增排队列表信息；前端需根据新状态展示"我要观战"按钮、"预约排队"按钮、排队位置等。

**技术方案**：
- `RoomState` DTO 新增 `QueueList []QueueInfo` 字段
- `QueueInfo` 包含：UserID、Nickname、Avatar、QueuePosition、QueuedAt
- 排队者自动替补成功后推送 `substitute` 事件通知前端
- 前端根据用户当前状态（已入座/纯观战/排队中）展示不同操作按钮

**涉及文件**：
| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/game/application/room_state.go` | 修改 | RoomState 新增排队列表 |
| `backend/game/domain/events.go` | 修改 | 新增排队事件、替补事件 |
| 前端相关文件 | 修改 | UI 适配（按钮、排队展示等） |

---

## 三、数据流路径

### 自动上座流程
```
Client → WebSocket(join_room) → Gateway → gRPC → GameService.JoinRoom
  → LuaJoinAndAutoSeat:
      if 空座位存在 and 余额充足:
          加入观战者 + 自动分配最低编号空座位 → 返回已入座状态
      else if 空座位存在 and 余额不足:
          加入观战者(无座位) → 返回纯观战状态 + 余额不足提示
      else:
          加入观战者(无座位) → 返回纯观战状态
  → 广播房间状态
```

### 我要观战流程
```
Client → WebSocket(switch_to_spectator) → Gateway → gRPC → SeatService.SwitchToSpectator
  → LuaCancelSeat (复用现有):
      释放座位 → 检查排队队列 → 触发自动替补
  → 广播房间状态
```

### 排队替补流程
```
Client → WebSocket(enqueue) → Gateway → gRPC → RoomService.Enqueue
  → LuaEnqueue:
      加入排队有序集合 → 返回排队位置
  → 广播房间状态（含队列信息）

座位释放时（CancelSeat/LeaveRoom）:
  → LuaAutoSubstitute:
      if 排队队列非空:
          取出队首排队者 → 分配释放的座位 → 从队列移除
          → 推送替补事件给替补者
          → 广播房间状态
      else:
          座位保持空位
```

## 四、人天计划

| 模块 | 功能 | 人天 |
|------|------|------|
| **模块1** | 入房自动上座 | 2人天 |
| | 1.1 设计并实现 LuaJoinAndAutoSeat 原子脚本 | 0.5人天 |
| | 1.2 修改 JoinRoom/AutoMatchAndJoin 应用服务 | 0.5人天 |
| | 1.3 实现 SwitchToSpectator（我要观战）命令 | 0.5人天 |
| | 1.4 命令路由与接口联调 | 0.5人天 |
| **模块2** | 满座自动观战 | 0.5人天 |
| | 2.1 JoinRoom 中满座判断逻辑 | 0.25人天 |
| | 2.2 余额不足时的降级处理 | 0.25人天 |
| **模块3** | 排队预约机制 | 3人天 |
| | 3.1 Redis 队列数据结构设计与 LuaEnqueue/LuaDequeue 脚本 | 0.5人天 |
| | 3.2 LuaAutoSubstitute 原子替补脚本 | 1人天 |
| | 3.3 CancelSeat/LeaveRoom 触发替补逻辑 | 0.5人天 |
| | 3.4 Enqueue/Dequeue 应用服务与命令路由 | 0.5人天 |
| | 3.5 断线重连、离开房间时队列清理 | 0.25人天 |
| | 3.6 替补时余额不足的异常处理 | 0.25人天 |
| **模块4** | 房间状态与前端适配 | 1.5人天 |
| | 4.1 RoomState DTO 扩展排队列表 | 0.25人天 |
| | 4.2 排队/替补事件定义与推送 | 0.25人天 |
| | 4.3 前端UI适配（按钮、排队展示、替补通知） | 1人天 |
| **联调测试** | 端到端集成测试 | 1人天 |
| | 5.1 并发场景测试（多人同时入房、排队、替补） | 0.5人天 |
| | 5.2 边界条件测试（断线、余额不足、中断状态） | 0.5人天 |
| **合计** | | **8人天** |

## 五、预期产出

1. 用户入房体验优化：空座时自动上座，无需手动选座
2. 灵活的角色切换：已入座用户可随时切换为观战
3. 排队替补机制：满座后观战者可排队，退出时自动替补
4. 完善的异常处理：余额不足、断线重连、中断状态等场景的健壮处理
