# 机器人系统 (Robot System) Spec

## Why

游戏刚开始推广，玩家上座率不高，导致真人玩家在房间中长时间等待，影响游戏体验和留存率。需要一个机器人系统模拟真人玩家补位，提高房间上座率和游戏启动效率。

核心目标：
- 当房间真人不足时，自动分配机器人补位，使游戏能够快速开始
- 机器人行为模拟真人（入座、准备、抢红包、发红包等），带随机延迟
- **机器人使用虚拟账号，所有资金操作（扣款/入账/结算/余额查询）均在游戏系统内部完成，不与资金平台交互，零平台 API 调用**
- 真人玩家的资金操作保持不变
- 调度规则与行为参数全部可配置，支持运行时热更新

## What Changes

### 新增模块
- **机器人账号管理**：批量创建机器人 User + RobotAccount 记录，维护虚拟余额（Redis 为主，DB 定期同步），管理账号池
- **机器人调度器**：定时巡检 Waiting 房间，按规则分配/回收机器人，含防抖与优先级排序
- **机器人行为引擎**：复用现有 TimeoutScheduler（Redis ZSET 持久化），监听游戏事件触发延迟行为（选座/准备/抢红包/发红包/离场）
- **机器人玩家 (RobotPlayer)**：封装对 SeatAppService/GameAppService/RoomAppService 的内部调用，绕过 WebSocket 层
- **机器人身份识别 (RobotChecker)**：Redis SET 实现 O(1) 查询，供结算层识别机器人
- **虚拟结算通道**：Settlement Service 4 个修改点（扣款/入账/结算/余额查询）为机器人提供虚拟通道，跳过平台 API
- **配置中心**：`game.yaml` 新增 `robot` 段，通过 Nacos 下发，支持热更新

### 修改点
- **BREAKING**：`users` 表新增 `is_robot` 字段（DB 层标识，默认 false）
- **BREAKING**：`bill_record` 表新增 `is_robot` 字段（运营统计用，默认 false）
- `Player` 结构新增 `IsRobot` 标记（内存层标识）
- 选座/加入房间 Lua 脚本传递 `is_robot` 标记
- `TimeoutScheduler` 新增 `TimeoutTypeRobot` 类型
- `endGameWithOptions` 触发机器人回收通知
- `GameEventConsumer` 消费事件时触发行为引擎
- `GrabService` 新增 `GetAvailablePacketID` 方法

## Impact

### Affected specs
- 资金结算流程（真人不变，机器人走虚拟通道）
- 房间调度流程（新增机器人补位）
- 游戏行为流程（新增机器人自动操作）
- 配置管理（新增 robot 段）

### Affected code
- `backend/game/model/user.go` — User 结构新增 IsRobot 字段
- `backend/game/model/robot_account.go` — 新建数据模型
- `backend/game/domain/room.go` — Player 结构新增 IsRobot 标记
- `backend/game/domain/robot_behavior.go` — 新建行为引擎核心逻辑
- `backend/game/application/robot_account_service.go` — 新建账号管理
- `backend/game/application/robot_scheduler_service.go` — 新建调度器
- `backend/game/application/robot_player.go` — 新建 RobotPlayer
- `backend/game/application/grab_service.go` — 新增 GetAvailablePacketID
- `backend/game/application/game_app_service.go` — endGameWithOptions 触发回收
- `backend/game/scheduler/timeout_scheduler.go` — 新增 TimeoutTypeRobot
- `backend/game/scheduler/robot_behavior_scheduler.go` — 新建行为调度集成
- `backend/game/scheduler/virtual_balance_sync.go` — 新建虚拟余额同步调度器
- `backend/game/bootstrap/container.go` — 注册机器人相关服务与处理器
- `backend/game/bootstrap/app.go` — 装配机器人服务
- `backend/game/infrastructure/persistence/mysql/robot_account_repo.go` — 新建数据库操作
- `backend/game/infrastructure/persistence/redis/robot_pool.go` — 新建账号池 Redis
- `backend/game/infrastructure/persistence/redis/virtual_balance.go` — 新建虚拟余额 Redis
- `backend/game/infrastructure/persistence/redis/robot_scheduler.go` — 新建调度状态 Redis
- `backend/game/infrastructure/persistence/redis/lua_scripts.go` — 选座/加入房间传递 is_robot
- `backend/game/infrastructure/messaging/game_event_consumer.go` — 消费事件触发行为引擎
- `backend/settlement/model/bill.go` — BillRecord 新增 IsRobot 字段
- `backend/settlement/service/robot_checker.go` — 新建 RobotChecker
- `backend/settlement/service/virtual_balance_service.go` — 新建虚拟余额服务（结算层调用）
- `backend/settlement/service/deduct_service.go` — executeSingleDeduct 新增虚拟通道分支
- `backend/settlement/service/game_settle_service.go` — executeSessionCredit + settlePlayer 新增虚拟通道分支
- `backend/settlement/service/settlement_service.go` — CheckBalance/GetUserBalance/creditRound 新增虚拟通道分支
- `backend/common/config/config.go` — Config 新增 Robot 配置段
- `backend/config/game.yaml` — 新增 robot 段配置
- `backend/scripts/init_robot_accounts.go` — 新建机器人账号初始化脚本

## ADDED Requirements

### Requirement: 机器人账号管理
系统 SHALL 支持批量创建机器人账号，每个机器人由 User 记录（合成平台 UserID `robot_<seq>`）+ RobotAccount 记录（1:1 关联）组成。User 表新增 `is_robot` 字段（bool，默认 false）用于 DB 层标识。机器人昵称/头像从名称库随机生成，存储在 User 表中。

#### Scenario: 批量创建机器人账号
- **WHEN** 运营执行 `init_robot_accounts.go` 脚本或调用管理 API 创建 N 个机器人
- **THEN** 系统为每个机器人创建 User 记录（`user_id = robot_<seq>`，`is_robot = true`）+ RobotAccount 记录（`UserID = User.ID`，`Status = 1` 空闲，`VirtualBalance = 档位要求 × initial_balance_multi`）
- **AND** 将 `User.ID` 加入 Redis SET `robot:user_ids`（供结算层 RobotChecker 识别）
- **AND** 设置 Redis `robot:virtual_balance:{User.ID}` 为初始虚拟余额
- **AND** 将 `User.ID` 加入 Redis SET `robot:pool:available`（可用账号池）

#### Scenario: 机器人账号状态管理
- **WHEN** 机器人虚拟余额低于 `low_balance_threshold × 档位要求`
- **THEN** 系统将 RobotAccount.Status 设为停用（3），从可用账号池移除
- **AND** 运营可通过管理 API 调增虚拟余额后重新激活

### Requirement: 虚拟余额服务
系统 SHALL 通过 Redis 原子操作（INCRBY）管理机器人虚拟余额，DB 定期同步脏数据。

#### Scenario: 虚拟扣款
- **WHEN** 调用 `VirtualBalanceService.Deduct(ctx, userID, amount)`
- **THEN** 系统执行 Redis `INCRBY robot:virtual_balance:{userID} -amount`
- **AND** 将 userID 加入 Redis SET `robot:virtual_balance:dirty`（标记需同步到 DB）
- **AND** 扣减后余额为负时返回错误（防御性检查）

#### Scenario: 虚拟入账
- **WHEN** 调用 `VirtualBalanceService.Credit(ctx, userID, amount)`
- **THEN** 系统执行 Redis `INCRBY robot:virtual_balance:{userID} +amount`
- **AND** 将 userID 加入 Redis SET `robot:virtual_balance:dirty`

#### Scenario: 查询虚拟余额
- **WHEN** 调用 `VirtualBalanceService.GetBalance(ctx, userID)`
- **THEN** 系统从 Redis `GET robot:virtual_balance:{userID}` 读取
- **AND** 缓存未命中时从 DB 加载并设置缓存

#### Scenario: 虚拟余额同步到 DB
- **WHEN** 定时任务（`sync_interval`，默认 30s）触发 `VirtualBalanceService.SyncToDB`
- **THEN** 系统读取 Redis SET `robot:virtual_balance:dirty` 中的所有 userID
- **AND** 对每个 userID 读取 Redis 虚拟余额，更新 DB `robot_accounts.virtual_balance`
- **AND** 清空 dirty SET

### Requirement: 机器人调度器
系统 SHALL 定时巡检（`scan_interval`，默认 5s）Waiting 状态房间，按规则分配机器人补位，游戏结束后回收机器人。

#### Scenario: 补位触发条件
- **WHEN** 巡检扫描到房间满足以下全部条件：
  - 房间状态 = `RoomStatusWaiting`（1）
  - 已准备真人玩家数 >= `MinRealPlayers`（默认 2）
  - 已选座总人数（含已准备 + 已选座未准备）< `MaxPlayers`（5）
- **THEN** 系统将该房间标记为需要补位

#### Scenario: 不补位的场景
- **WHEN** 房间为纯旁观者房间（无已准备真人）
- **OR** 房间已满员（5 座全占）
- **OR** 房间游戏进行中
- **THEN** 系统不分配机器人

#### Scenario: 机器人分配数量计算
- **WHEN** 房间需要补位
- **THEN** 需要机器人数 = `MaxPlayers - 已选座总人数`（按占座数算，非准备数）
- **AND** 实际分配数 = `min(需要机器人数, MaxRobotsPerRoom - 房间已有机器人数, 可用机器人池数量)`

#### Scenario: 机器人选择策略
- **WHEN** 从账号池选择机器人
- **THEN** 机器人虚拟余额 >= 该档位余额要求（`roomFee/5 + roomFee * 9`）
- **AND** 机器人 `MinRoomFee <= 房间RoomFee <= MaxRoomFee`
- **AND** 余额最低的机器人优先分配
- **AND** 排除已游戏中、已停用、余额不足的机器人

#### Scenario: 房间优先级排序
- **WHEN** 可用机器人不足以满足所有待补位房间
- **THEN** 系统按多级排序分配：第一级按已准备真人数降序（差1人就能开局优先），第二级按等待时长升序（等得久的优先）

#### Scenario: 分配防抖
- **WHEN** 分配机器人
- **THEN** 系统获取分配锁 `robot:assign:{userID}`（TTL `robot_assign_lock_ttl`，默认 10s）防止同一机器人被同时分配到多个房间
- **AND** 获取房间限流锁 `robot:room_assign:{roomID}`（TTL `room_assign_lock_ttl`，默认 30s）防止同一房间短时间重复分配
- **AND** 机器人回收后 `recycle_cooldown`（默认 60s）内不重新分配
- **AND** 账号池保留 `reserve_count`（默认 5）个机器人（不超过总池 30%）

#### Scenario: 机器人回收
- **WHEN** 游戏结束（`endGameWithOptions` 调用或 Kafka `session_end` 事件）
- **THEN** 系统查询房间内机器人列表（Redis SET `robot:room:{roomID}`）
- **AND** 对每个机器人：取消行为调度任务 → 延迟 3-10s 离场 → 更新状态为空闲 → 归还账号池 → 删除映射
- **AND** 定时巡检兜底检测已结束房间的残留机器人

### Requirement: 机器人行为引擎
系统 SHALL 复用现有 TimeoutScheduler（Redis ZSET 持久化）调度机器人延迟行为，监听游戏事件触发行为。

#### Scenario: 选座+准备行为（分配触发）
- **WHEN** 调度器分配机器人到房间
- **THEN** 调用 `RobotPlayer.JoinAndReady`：入房为旁观者 → 检查空座 → 调度延迟选座（`seat_delay_min`~`seat_delay_max`，默认 2-5s）
- **AND** 选座成功后链式调度延迟准备（`ready_delay_min`~`ready_delay_max`，默认 1-3s）

#### Scenario: 抢红包行为（事件触发）
- **WHEN** 监听到 Kafka `packet_created` 事件
- **THEN** 检查该房间内是否有机器人
- **AND** 对每个机器人：以 `grab_skip_prob`（默认 0.05）概率决定是否跳过
- **AND** 若决定抢：随机延迟 `grab_delay_min`~`grab_delay_max`（默认 1-8s）后调用 `RobotPlayer.GrabPacket`

#### Scenario: 发红包行为（事件触发）
- **WHEN** 监听到 Kafka `round_settle` 事件
- **THEN** 从事件解析最小金额获得者（MinPlayerID）
- **AND** 检查 MinPlayerID 是否为机器人
- **AND** 若是机器人：随机延迟 `send_delay_min`~`send_delay_max`（默认 2-5s）后调用 `RobotPlayer.SendPacket`
- **AND** 延迟必须 < 现有 SendTimeout（30s），确保超时前完成

#### Scenario: 离场行为（事件触发）
- **WHEN** 监听到 Kafka `session_end` 事件或 GameEnd 回调
- **THEN** 查询该房间内所有机器人
- **AND** 对每个机器人：随机延迟 `leave_after_game_min`~`leave_after_game_max`（默认 3-10s）后调用 `RobotPlayer.LeaveRoom`
- **AND** 更新 RobotAccount 状态为空闲，归还到 RobotPool

#### Scenario: 延迟实现方式
- **WHEN** 调度机器人行为
- **THEN** 系统使用 `TimeoutTypeRobot` 类型注册到现有 TimeoutScheduler
- **AND** 通过 `SetTimeout(ctx, TimeoutTypeRobot, roomID, data, delay)` 注册延迟任务
- **AND** data 格式为 `robotUserID:action:uuid`（action ∈ seat/ready/grab/send/leave）
- **AND** 服务重启后定时任务不丢失（Redis ZSET 持久化）

### Requirement: 机器人玩家 (RobotPlayer)
系统 SHALL 提供 RobotPlayer 封装对现有游戏服务的内部调用，绕过 WebSocket 层。

#### Scenario: JoinAndReady
- **WHEN** 调度器调用 `RobotPlayer.JoinAndReady(ctx, roomID, robotUserID)`
- **THEN** 调用 `RoomAppService.JoinRoom` 入房为旁观者
- **AND** 检查是否有空座（竞态保护），无空座返回 `ErrNoEmptySeat`
- **AND** 调度延迟选座（由行为引擎经 TimeoutScheduler 延迟触发）

#### Scenario: SelectSeat
- **WHEN** 行为引擎延迟调用 `RobotPlayer.SelectSeat(ctx, roomID, robotUserID)`
- **THEN** 查询 RoomStateData.SeatOwners 找空座，随机选择一个
- **AND** 调用 `SeatAppService.SelectSeat(roomID, robotUserID, seatNo)`
- **AND** 选座成功后链式调度延迟准备

#### Scenario: Ready
- **WHEN** 行为引擎延迟调用 `RobotPlayer.Ready(ctx, roomID, robotUserID)`
- **THEN** 调用 `SeatAppService.PlayerReady(roomID, robotUserID)`

#### Scenario: GrabPacket
- **WHEN** 行为引擎延迟调用 `RobotPlayer.GrabPacket(ctx, roomID, roundID, robotUserID)`
- **THEN** 调用 `GrabService.GetAvailablePacketID` 获取可用红包 ID
- **AND** 调用 `GameAppService.GrabPacket(roomID, robotUserID, packetID)`

#### Scenario: SendPacket
- **WHEN** 行为引擎延迟调用 `RobotPlayer.SendPacket(ctx, roomID, robotUserID)`
- **THEN** 调用 `GameAppService.SendPacket(roomID, robotUserID)`

#### Scenario: LeaveRoom
- **WHEN** 行为引擎/调度器调用 `RobotPlayer.LeaveRoom(ctx, roomID, robotUserID)`
- **THEN** 调用 `SeatAppService.CancelSeat(roomID, robotUserID)`
- **AND** 调用 `RoomAppService.LeaveRoom(roomID, robotUserID)`

### Requirement: 机器人身份识别 (RobotChecker)
系统 SHALL 在结算层提供 RobotChecker 接口，基于 Redis SET 实现 O(1) 查询。

#### Scenario: 机器人身份判断
- **WHEN** 结算层调用 `RobotChecker.IsRobot(ctx, userID int64)`
- **THEN** 系统执行 Redis `SISMEMBER robot:user_ids {userID}`
- **AND** 返回 bool（存在为 true）

#### Scenario: 三层身份标识
- **WHEN** 机器人在系统中流转
- **THEN** DB 层：`users.is_robot`（bool）用于 DB 查询过滤、运营统计、Redis 兜底重建
- **AND** Redis 层：`robot:user_ids` SET 用于结算热路径 O(1) 查询
- **AND** 内存层：`Player.IsRobot`（bool）用于游戏内调度器/行为引擎识别

### Requirement: 虚拟结算通道
系统 SHALL 在 Settlement Service 的 4 个资金操作路径为机器人提供虚拟通道，跳过平台 API 调用。

#### Scenario: 扣款虚拟通道
- **WHEN** `DeductService.executeSingleDeduct` 被调用且 `RobotChecker.IsRobot(userID)` 为 true
- **THEN** 调用 `VirtualBalanceService.Deduct(userID, amount)` 扣减虚拟余额
- **AND** BillRecord 创建时 `IsRobot = true`，Status 直接设为 Success
- **AND** 调用 `billMgr.UpdateBillSuccess(billID, 0, virtualBalanceAfter)`
- **AND** 跳过平台 API 调用（不调用 `platform.Debit`）
- **ELSE**（非机器人）原流程不变

#### Scenario: 入账虚拟通道
- **WHEN** `GameSettleService.executeSessionCredit` 被调用且 `RobotChecker.IsRobot(userID)` 为 true
- **THEN** 调用 `VirtualBalanceService.Credit(userID, bill.Amount)` 增加虚拟余额
- **AND** BillRecord 创建时 `IsRobot = true`，Status 直接设为 Success
- **AND** 调用 `billMgr.UpdateBillSuccess(billID, 0, virtualBalanceAfter)`
- **AND** 跳过平台 API 调用（不调用 `platform.Credit`）
- **ELSE**（非机器人）原流程不变

#### Scenario: 结算虚拟通道
- **WHEN** `GameSettleService.settlePlayer` 被调用且 `RobotChecker.IsRobot(userID)` 为 true
- **THEN** 调用 `billMgr.UpdateGameSettleStatusByUser(sessionID, userID, BillGameSettleSettled)`
- **AND** 跳过平台 API 调用（不调用 `platform.Settle`）
- **ELSE**（非机器人）原流程不变

#### Scenario: 余额查询虚拟通道
- **WHEN** `SettlementService.CheckBalance` 或 `GetUserBalance` 被调用且 `RobotChecker.IsRobot(userID)` 为 true
- **THEN** 调用 `VirtualBalanceService.GetBalance(userID)` 读取虚拟余额
- **AND** 返回虚拟余额及 `balance >= requiredAmount` 判断
- **AND** 跳过平台 API 调用（不调用 `platform.GetBalance`）
- **ELSE**（非机器人）原流程不变

### Requirement: 配置管理
系统 SHALL 通过 `game.yaml` 的 `robot` 段管理所有调度规则与行为参数，通过 Nacos 下发，支持运行时热更新。

#### Scenario: 配置加载
- **WHEN** Game Service 启动
- **THEN** 系统从 `game.yaml` 的 `robot` 段加载配置到 `RobotConfig` 结构
- **AND** 通过 Nacos 监听支持运行时热更新

#### Scenario: 配置校验
- **WHEN** 启动时加载配置
- **THEN** 校验 `MaxRobotsPerRoom + MinRealPlayers <= MaxPlayers(5)`，不满足则自动修正 MaxRobotsPerRoom 并告警
- **AND** 校验 `ReserveCount / 总池数 <= ReserveRatioMax`，超过则告警（不阻断启动）
- **AND** 校验所有 `DelayMin < 对应 DelayMax`，不满足则交换并告警
- **AND** 校验 `SendDelayMax < 现有 send 超时(30s)`，超过则告警
- **AND** 校验 `GrabSkipProb` 在 [0,1] 范围，超出则 clamp
- **AND** 校验 `InitialBalanceMulti >= 1.0`，不满足则设为 1.0 并告警

#### Scenario: 机器人总开关
- **WHEN** `robot.enabled = false`
- **THEN** 调度器不启动，不分配任何机器人

## MODIFIED Requirements

### Requirement: User 数据模型
User 表新增 `IsRobot` 字段（bool，默认 false），用于 DB 层面区分机器人与真人。

```go
type User struct {
    // ... 原有字段 ...
    IsRobot bool `json:"is_robot" gorm:"default:false;index"`
}
```

### Requirement: Player 数据结构
Player 结构新增 `IsRobot` 标记，用于游戏内调度器/行为引擎识别机器人身份。

```go
type Player struct {
    // ... 原有字段 ...
    IsRobot bool `json:"is_robot"`
}
```

### Requirement: BillRecord 数据模型
BillRecord 新增 `IsRobot` 字段，用于运营侧区分机器人和真人账单，做独立的盈亏统计（不影响结算逻辑）。

```go
type BillRecord struct {
    // ... 原有字段 ...
    IsRobot bool `gorm:"default:false;index" json:"is_robot"`
}
```

### Requirement: TimeoutScheduler
TimeoutScheduler 新增 `TimeoutTypeRobot` 类型，与现有 5 个类型（seat/ready/grab/send/replace）并列，用于调度机器人延迟行为。

```go
const TimeoutTypeRobot TimeoutType = "robot"
```

### Requirement: GameEventConsumer
GameEventConsumer 消费 `packet_created`、`round_settle`、`session_end` 事件时，触发机器人行为引擎执行对应行为。

### Requirement: endGameWithOptions
`GameAppService.endGameWithOptions` 在游戏结束时触发机器人回收通知（方式一：事件驱动实时回收）。

### Requirement: GrabService
GrabService 新增 `GetAvailablePacketID(ctx, roomID, roundID)` 方法，查询可用红包 ID 供机器人使用。

### Requirement: 选座/加入房间 Lua 脚本
选座/加入房间 Lua 脚本传递 `is_robot` 标记，确保机器人 Player 结构中 IsRobot 字段正确设置。
