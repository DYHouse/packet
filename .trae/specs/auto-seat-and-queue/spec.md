# 自动上座与排队替补机制 Spec

## Why
当前用户进入房间后始终先成为观战者，需手动执行 select_seat → player_ready 两步操作才能成为玩家；座位满后新观战者无法参与游戏，也没有排队替补机制；玩家退出后释放的座位无人自动补充。需要优化入房体验并实现排队替补。

## What Changes
- 修改 JoinRoom 逻辑：空座时自动分配座位并自动准备，满座时自动成为观战者
- 新增 LuaJoinAndAutoSeat 原子脚本，将"加入房间+自动选座+自动准备"合并为原子操作
- 新增 Redis 有序集合实现排队队列（LuaEnqueue、LuaDequeue、LuaAutoSubstitute）
- 新增 WebSocket 命令 `enqueue`（预约排队）和 `dequeue`（取消排队）
- 修改 CancelSeat 和 LeaveRoom 逻辑，在释放座位后触发自动替补
- 扩展 RoomState DTO，新增排队列表信息
- 新增排队/替补相关事件类型和推送

## Impact
- Affected specs: 房间入房流程、座位管理、房间状态广播
- Affected code:
  - `backend/game/infrastructure/persistence/redis/lua_scripts.go` — 新增4个Lua脚本
  - `backend/game/infrastructure/persistence/redis/keys.go` — 新增队列Key
  - `backend/game/infrastructure/persistence/redis/repository.go` — 新增仓储方法
  - `backend/game/domain/repository.go` — 新增接口定义和数据结构
  - `backend/game/domain/events.go` — 新增事件类型
  - `backend/game/domain/room.go` — 新增排队者数据结构
  - `backend/game/application/room_app_service.go` — 修改JoinRoom、LeaveRoom，新增Enqueue/Dequeue
  - `backend/game/application/seat_app_service.go` — CancelSeat后触发替补
  - `backend/game/application/room_state.go` — RoomState新增排队列表
  - `backend/game/server/generic_service.go` — 新增enqueue/dequeue命令路由
  - `backend/gateway/router/router.go` — 无需修改（路由基于yaml配置）
  - `backend/config/gateway-router.yaml` — 新增enqueue/dequeue路由
  - `backend/common/message/types.go` — 新增命令和推送常量
  - `backend/common/message/errors.go` — 新增错误码

## ADDED Requirements

### Requirement: 入房自动上座
系统 SHALL 在用户加入房间时，若房间有空座位且用户余额充足且房间状态为 Waiting，自动分配最低编号的空座位并自动标记为"已准备"状态。

#### Scenario: 空座自动上座成功
- **WHEN** 用户通过 join_room 或 auto_match 加入房间
- **AND** 房间有空座位
- **AND** 用户余额充足
- **AND** 房间状态为 Waiting
- **THEN** 系统自动分配最低编号的空座位，用户自动成为已入座已准备状态
- **AND** 返回结果中 is_spectator 为 false，seat_no 为分配的座位号

#### Scenario: 满座自动观战
- **WHEN** 用户通过 join_room 或 auto_match 加入房间
- **AND** 房间所有座位已被占用
- **THEN** 用户自动成为纯观战者（seat_no=0）
- **AND** 返回结果中 is_spectator 为 true

#### Scenario: 余额不足降级为观战
- **WHEN** 用户通过 join_room 或 auto_match 加入房间
- **AND** 房间有空座位
- **AND** 用户余额不足
- **THEN** 用户自动成为纯观战者
- **AND** 返回结果中 is_spectator 为 true，并附带余额不足提示

#### Scenario: 游戏进行中入房
- **WHEN** 用户通过 join_room 加入房间
- **AND** 房间状态为 Playing 或 Interrupted
- **THEN** 用户自动成为纯观战者，不自动上座

### Requirement: 我要观战
系统 SHALL 允许已入座的用户通过 cancel_seat 命令放弃座位变为纯观战者。

#### Scenario: 已入座用户切换为观战
- **WHEN** 已入座用户调用 cancel_seat
- **AND** 房间状态为 Waiting
- **THEN** 用户释放座位，变为纯观战者
- **AND** 触发排队替补逻辑

### Requirement: 排队预约机制
系统 SHALL 提供排队预约机制，观战者可选择"预约排队"，当有玩家退出释放座位时按排队顺序自动替补上座。

#### Scenario: 观战者预约排队
- **WHEN** 纯观战者调用 enqueue 命令
- **THEN** 系统将观战者加入排队有序集合，使用时间戳排序
- **AND** 返回排队位置
- **AND** 广播房间状态（含队列信息）

#### Scenario: 观战者取消排队
- **WHEN** 排队中的观战者调用 dequeue 命令
- **THEN** 系统将观战者从排队队列中移除
- **AND** 广播房间状态（含队列信息）

#### Scenario: 座位释放触发自动替补
- **WHEN** 有玩家/已入座者退出释放座位（CancelSeat 或 LeaveRoom）
- **AND** 排队队列非空
- **THEN** 系统从队列头部取出下一个排队者，自动分配释放的座位并自动准备
- **AND** 推送替补事件给替补者
- **AND** 广播房间状态

#### Scenario: 替补时排队者余额不足
- **WHEN** 座位释放触发自动替补
- **AND** 队首排队者余额不足
- **THEN** 跳过该排队者，通知余额不足
- **AND** 继续替补下一位排队者

#### Scenario: 队列为空时释放座位
- **WHEN** 有玩家退出释放座位
- **AND** 排队队列为空
- **THEN** 座位变为空位，等待新入房者自动上座

#### Scenario: 排队者中途离开房间
- **WHEN** 排队中的观战者离开房间
- **THEN** 系统自动从队列中移除该排队者

### Requirement: 房间状态广播扩展
系统 SHALL 在房间状态中包含排队列表信息，并在替补成功时推送替补事件。

#### Scenario: RoomState 包含排队列表
- **WHEN** 前端获取房间状态
- **THEN** RoomState 中包含 QueueList 字段，每个排队者包含 UserID、Nickname、Avatar、QueuePosition、QueuedAt

#### Requirement: 替补成功推送
- **WHEN** 排队者自动替补成功
- **THEN** 系统推送 substitute 事件给替补者
- **AND** 广播房间状态给房间内所有用户

### Requirement: 机器人系统适配
系统 SHALL 在自动上座功能上线后正确适配机器人入房流程。

#### Scenario: 机器人自动上座成功
- **WHEN** 机器人调用 JoinAndReady 加入房间
- **AND** 房间有空座位
- **THEN** JoinRoom 自动上座并准备，返回 IsSpectator=false
- **AND** 机器人不再调度 seat/ready 延迟动作

#### Scenario: 机器人满座入房
- **WHEN** 机器人调用 JoinAndReady 加入房间
- **AND** 房间座位已满
- **THEN** JoinRoom 返回 IsSpectator=true
- **AND** 机器人返回 ErrNoEmptySeat，不加入排队队列

#### Scenario: 机器人离开房间与替补兼容
- **WHEN** 机器人调用 LeaveRoom
- **THEN** CancelSeat 释放座位并触发排队替补
- **AND** LeaveRoom 清理数据并从队列移除

## MODIFIED Requirements

### Requirement: JoinRoom 流程
原流程：用户入房 → 仅成为观战者 → 手动 select_seat → 手动 player_ready
新流程：用户入房 → 判断空座+余额+状态 → 自动上座并准备 / 成为观战者

### Requirement: CancelSeat 流程
原流程：用户取消座位 → 释放座位 → 广播
新流程：用户取消座位 → 释放座位 → 触发排队替补 → 广播

### Requirement: LeaveRoom 流程
原流程：用户离开房间 → 清理数据 → 广播
新流程：用户离开房间 → 清理数据（含队列清理）→ 触发排队替补 → 广播
