# Tasks

## 阶段一：数据模型与基础设施

- [x] Task 1: User 模型新增 IsRobot 字段
  - [x] SubTask 1.1: 修改 `backend/game/model/user.go`，User 结构新增 `IsRobot bool` 字段（gorm:"default:false;index"）
  - [x] SubTask 1.2: 修改 `backend/game/infrastructure/persistence/mysql/user_repository.go`（如需要），确保 CreateOrUpdateUser 正确处理 IsRobot 字段
  - [x] SubTask 1.3: 确认 GORM AutoMigrate 能自动添加新字段（检查 bootstrap/app.go 中 AutoMigrate 调用）

- [x] Task 2: 创建 RobotAccount 数据模型
  - [x] SubTask 2.1: 新建 `backend/game/model/robot_account.go`，定义 RobotAccount 结构（ID, UserID, Status, VirtualBalance, TotalVirtualDebit, TotalVirtualCredit, MinRoomFee, MaxRoomFee, TotalGames, TotalProfit, LastActiveAt, CreatedAt, UpdatedAt）
  - [x] SubTask 2.2: 定义 RobotAccount 状态常量（0=未激活 1=空闲 2=游戏中 3=停用）
  - [x] SubTask 2.3: 在 bootstrap/app.go 的 AutoMigrate 调用中注册 RobotAccount 模型

- [x] Task 3: 创建 RobotAccount 数据库仓库
  - [x] SubTask 3.1: 新建 `backend/game/infrastructure/persistence/mysql/robot_account_repo.go`
  - [x] SubTask 3.2: 实现 CRUD 方法：Create、GetByUserID、UpdateStatus、UpdateBalance、GetAvailableRobots（按 MinRoomFee/MaxRoomFee/Status 过滤，按 VirtualBalance 升序）
  - [x] SubTask 3.3: 实现批量创建方法 BatchCreate

- [x] Task 4: BillRecord 模型新增 IsRobot 字段
  - [x] SubTask 4.1: 修改 `backend/settlement/model/bill.go`，BillRecord 结构新增 `IsRobot bool` 字段（gorm:"default:false;index"）
  - [x] SubTask 4.2: 确认 AutoMigrate 能自动添加新字段

- [x] Task 5: Player 结构新增 IsRobot 标记
  - [x] SubTask 5.1: 修改 `backend/game/domain/room.go`，Player 结构新增 `IsRobot bool` 字段
  - [x] SubTask 5.2: 修改 `backend/game/application/room_state.go`，PlayerInfo 和 SeatInfo 结构新增 IsRobot 字段（如需广播给前端）

## 阶段二：Redis 存储与虚拟余额

- [x] Task 6: 创建虚拟余额 Redis 服务
  - [x] SubTask 6.1: 新建 `backend/game/infrastructure/persistence/redis/virtual_balance.go`
  - [x] SubTask 6.2: 定义 Redis key 常量：`robot:virtual_balance:{userID}`、`robot:virtual_balance:dirty`、`robot:user_ids`
  - [x] SubTask 6.3: 实现 Deduct 方法（INCRBY 原子扣减 + SADD dirty + 余额为负检查）
  - [x] SubTask 6.4: 实现 Credit 方法（INCRBY 原子增加 + SADD dirty）
  - [x] SubTask 6.5: 实现 GetBalance 方法（GET，缓存未命中从 DB 加载）
  - [x] SubTask 6.6: 实现 SyncToDB 方法（SMEMBERS dirty + GET 余额 + UPDATE DB + DEL dirty）
  - [x] SubTask 6.7: 实现 AddToRobotSet 方法（SADD robot:user_ids，供 RobotChecker 使用）

- [x] Task 7: 创建机器人账号池 Redis 服务
  - [x] SubTask 7.1: 新建 `backend/game/infrastructure/persistence/redis/robot_pool.go`
  - [x] SubTask 7.2: 实现可用账号池操作：AddToAvailablePool、RemoveFromAvailablePool、GetAvailableCount
  - [x] SubTask 7.3: 实现按房间费用等级匹配机器人（结合 DB 查询 MinRoomFee/MaxRoomFee + Redis 虚拟余额检查）

- [x] Task 8: 创建调度器状态 Redis 服务
  - [x] SubTask 8.1: 新建 `backend/game/infrastructure/persistence/redis/robot_scheduler.go`
  - [x] SubTask 8.2: 实现 Redis key 常量：`robot:room:{roomID}`、`robot:assign:{robotUserID}`、`robot:room_assign:{roomID}`、`robot:recycle_cooldown:{userID}`、`robot:scheduler:active`
  - [x] SubTask 8.3: 实现房间-机器人映射操作（SADD/SMEMBERS/SREM robot:room:{roomID}）
  - [x] SubTask 8.4: 实现分配锁操作（SET NX + TTL）
  - [x] SubTask 8.5: 实现房间限流锁操作（SET NX + TTL）
  - [x] SubTask 8.6: 实现回收冷却标记操作（SET + TTL）
  - [x] SubTask 8.7: 实现活跃机器人集合操作（SADD/SISMEMBER robot:scheduler:active）

## 阶段三：配置管理

- [x] Task 9: 新增 Robot 配置结构
  - [x] SubTask 9.1: 修改 `backend/common/config/config.go`，Config 结构新增 `Robot RobotConfig` 字段
  - [x] SubTask 9.2: 定义 RobotConfig 结构（Enabled, Scheduler, Behavior, Account 子结构）
  - [x] SubTask 9.3: 定义 SchedulerConfig（ScanInterval, MinRealPlayers, MaxRobotsPerRoom, RobotAssignLockTTL, RoomAssignLockTTL, RecycleCooldown, ReserveCount, ReserveRatioMax）
  - [x] SubTask 9.4: 定义 BehaviorConfig（SeatDelayMin/Max, ReadyDelayMin/Max, GrabDelayMin/Max, GrabSkipProb, SendDelayMin/Max, LeaveAfterGameMin/Max）
  - [x] SubTask 9.5: 定义 AccountConfig（InitialBalanceMulti, LowBalanceThreshold, SyncInterval）
  - [x] SubTask 9.6: 在 defaults.go 中添加 Robot 配置默认值

- [x] Task 10: game.yaml 新增 robot 段
  - [x] SubTask 10.1: 修改 `backend/config/game.yaml`，新增 robot 段配置（enabled, scheduler, behavior, account）
  - [x] SubTask 10.2: 按文档 4.1 章节配置默认值

- [x] Task 11: 实现配置校验
  - [x] SubTask 11.1: 新建 `backend/game/application/robot_config.go`（或在 config 包中），实现 ValidateRobotConfig 函数
  - [x] SubTask 11.2: 校验 MaxRobotsPerRoom + MinRealPlayers <= 5，不满足则自动修正并告警
  - [x] SubTask 11.3: 校验 ReserveCount / 总池数 <= ReserveRatioMax，超过则告警
  - [x] SubTask 11.4: 校验所有 DelayMin < DelayMax，不满足则交换并告警
  - [x] SubTask 11.5: 校验 SendDelayMax < 30s，超过则告警
  - [x] SubTask 11.6: 校验 GrabSkipProb 在 [0,1]，超出则 clamp
  - [x] SubTask 11.7: 校验 InitialBalanceMulti >= 1.0，不满足则设为 1.0 并告警

## 阶段四：机器人账号管理服务

- [x] Task 12: 创建 RobotAccountService
  - [x] SubTask 12.1: 新建 `backend/game/application/robot_account_service.go`
  - [x] SubTask 12.2: 实现 BatchCreateRobots 方法（生成合成 UserID robot_<seq> + 随机昵称/头像 + 调用 UserService.SaveUser + 设置 is_robot=true + 创建 RobotAccount + 初始化 Redis 虚拟余额 + 加入账号池）
  - [x] SubTask 12.3: 实现 GetAvailableRobot 方法（按房间费用等级匹配，余额最低优先）
  - [x] SubTask 12.4: 实现 MarkRobotInGame / MarkRobotIdle 方法（更新状态 + 账号池操作）
  - [x] SubTask 12.5: 实现 CheckLowBalance 方法（虚拟余额低于阈值时停用）
  - [x] SubTask 12.6: 实现 RechargeVirtualBalance 方法（管理 API 调增虚拟余额）

- [x] Task 13: 创建虚拟余额同步调度器
  - [x] SubTask 13.1: 新建 `backend/game/scheduler/virtual_balance_sync.go`
  - [x] SubTask 13.2: 实现定时调用 VirtualBalanceService.SyncToDB（间隔由 account.sync_interval 配置）
  - [x] SubTask 13.3: 实现 Start/Stop 方法

- [x] Task 14: 创建机器人账号初始化脚本
  - [x] SubTask 14.1: 新建 `backend/scripts/init_robot_accounts.go`
  - [x] SubTask 14.2: 实现批量创建机器人逻辑（参考 init_rooms.go 模式）
  - [x] SubTask 14.3: 支持命令行参数指定创建数量、初始余额倍数
  - [x] SubTask 14.4: 创建后输出统计信息

## 阶段五：机器人玩家 (RobotPlayer)

- [x] Task 15: 创建 RobotPlayer 核心实现
  - [x] SubTask 15.1: 新建 `backend/game/application/robot_player.go`
  - [x] SubTask 15.2: 定义 RobotPlayer 结构（seatAppService, gameAppService, roomAppService, accountSvc, grabSvc, repo, behaviorEngine, config）
  - [x] SubTask 15.3: 实现 JoinAndReady 方法（入房为旁观者 + 检查空座 + 调度延迟选座）
  - [x] SubTask 15.4: 实现 SelectSeat 方法（查询空座 + 随机选择 + 调用 SeatAppService.SelectSeat + 链式调度延迟准备）
  - [x] SubTask 15.5: 实现 Ready 方法（调用 SeatAppService.PlayerReady）
  - [x] SubTask 15.6: 实现 GrabPacket 方法（调用 GrabService.GetAvailablePacketID + GameAppService.GrabPacket）
  - [x] SubTask 15.7: 实现 SendPacket 方法（调用 GameAppService.SendPacket）
  - [x] SubTask 15.8: 实现 LeaveRoom 方法（调用 SeatAppService.CancelSeat + RoomAppService.LeaveRoom）

- [x] Task 16: GrabService 新增 GetAvailablePacketID 方法
  - [x] SubTask 16.1: 修改 `backend/game/application/grab_service.go`，新增 GetAvailablePacketID(ctx, roomID, roundID) 方法
  - [x] SubTask 16.2: 从 Redis 查询可用红包列表（RoundAvailablePacketsKey），返回第一个可用 packetID

## 阶段六：机器人行为引擎

- [x] Task 17: TimeoutScheduler 新增 TimeoutTypeRobot
  - [x] SubTask 17.1: 修改 `backend/game/scheduler/timeout_scheduler.go`，新增 `TimeoutTypeRobot TimeoutType = "robot"` 常量
  - [x] SubTask 17.2: 在 NewTimeoutScheduler 中为 TimeoutTypeRobot 设置默认 check interval（如 500ms）

- [x] Task 18: 创建机器人行为引擎核心
  - [x] SubTask 18.1: 新建 `backend/game/domain/robot_behavior.go`（或 application 包），定义 RobotBehaviorEngine 结构
  - [x] SubTask 18.2: 实现 scheduleRobotAction 方法（注册 TimeoutTypeRobot 延迟任务，data 格式 `robotUserID:action:uuid`）
  - [x] SubTask 18.3: 实现 HandleRobotTimeout 方法（解析 data，按 action 分发到 RobotPlayer 对应方法）
  - [x] SubTask 18.4: 实现随机延迟计算工具函数 randomDelay(min, max)
  - [x] SubTask 18.5: 实现抢红包跳过概率判断 shouldSkipGrab(prob)

- [x] Task 19: 创建机器人行为调度集成
  - [x] SubTask 19.1: 新建 `backend/game/scheduler/robot_behavior_scheduler.go`（如需要，封装行为引擎与 TimeoutScheduler 的集成）
  - [x] SubTask 19.2: 实现事件监听触发行为引擎（packet_created → 抢红包，round_settle → 发红包，session_end → 离场）

- [x] Task 20: GameEventConsumer 集成行为引擎
  - [x] SubTask 20.1: 修改 `backend/game/infrastructure/messaging/game_event_consumer.go`，注入 RobotBehaviorEngine
  - [x] SubTask 20.2: handlePacketCreated 中触发行为引擎抢红包逻辑
  - [x] SubTask 20.3: handleRoundSettle 中触发行为引擎发红包逻辑
  - [x] SubTask 20.4: handleSessionEnd 中触发行为引擎离场逻辑

## 阶段七：机器人调度器

- [x] Task 21: 创建 RobotSchedulerService
  - [x] SubTask 21.1: 新建 `backend/game/application/robot_scheduler_service.go`
  - [x] SubTask 21.2: 定义 RobotSchedulerService 结构（accountSvc, robotPlayer, repo, dbRepo, redis, config, ctx, cancel）
  - [x] SubTask 21.3: 实现巡检主循环 Start（按 scan_interval 定时执行 scanRooms）
  - [x] SubTask 21.4: 实现 scanRooms 方法（获取 Waiting 房间 → 过滤需要补位的 → 按优先级排序 → 分配机器人）
  - [x] SubTask 21.5: 实现房间优先级排序 sortRoomsByPriority（已准备真人数降序 + 等待时长升序）
  - [x] SubTask 21.6: 实现分配机器人 assignRobotsToRoom（检查限流锁 → 计算需要机器人数 → 获取空闲机器人 → 获取分配锁 → 调用 RobotPlayer.JoinAndReady）
  - [x] SubTask 21.7: 实现时间预算控制（单次巡检不超过 ScanInterval 的 80%）

- [x] Task 22: 实现机器人回收
  - [x] SubTask 22.1: 实现 OnGameEnd 方法（事件驱动回收：查询房间机器人 → 取消行为调度 → 延迟离场 → 更新状态 → 归还账号池）
  - [x] SubTask 22.2: 实现定时巡检兜底回收（检测已结束房间的残留机器人）
  - [x] SubTask 22.3: 修改 `backend/game/application/game_app_service.go`，endGameWithOptions 中触发 RobotSchedulerService.OnGameEnd

- [x] Task 23: 实现账号池余量监控
  - [x] SubTask 23.1: 巡检循环中检查账号池余量，低于 ReserveCount 时记录告警日志
  - [x] SubTask 23.2: 启动时校验 ReserveCount 与总池数关系，超过 50% 则告警

## 阶段八：结算层虚拟通道

- [x] Task 24: 创建 RobotChecker
  - [x] SubTask 24.1: 新建 `backend/settlement/service/robot_checker.go`
  - [x] SubTask 24.2: 定义 RobotChecker 接口（IsRobot(ctx, userID int64) bool）
  - [x] SubTask 24.3: 实现 redisRobotChecker（SISMEMBER robot:user_ids）

- [x] Task 25: 创建结算层 VirtualBalanceService
  - [x] SubTask 25.1: 新建 `backend/settlement/service/virtual_balance_service.go`（或复用 game 层的 VirtualBalanceService）
  - [x] SubTask 25.2: 实现 Deduct/Credit/GetBalance 方法（调用 Redis 操作）
  - [x] SubTask 25.3: 确保结算层能访问 game 层的 Redis 客户端

- [x] Task 26: DeductService 新增虚拟通道
  - [x] SubTask 26.1: 修改 `backend/settlement/service/deduct_service.go`，DeductService 结构新增 robotChecker 和 virtualBalance 字段
  - [x] SubTask 26.2: 修改 NewDeductService 构造函数，注入 robotChecker 和 virtualBalance
  - [x] SubTask 26.3: 修改 executeSingleDeduct 方法，开头新增机器人分支：if robotChecker.IsRobot → virtualBalance.Deduct + BillRecord IsRobot=true + UpdateBillSuccess + return
  - [x] SubTask 26.4: 确保非机器人走原流程不变

- [x] Task 27: GameSettleService 新增虚拟通道
  - [x] SubTask 27.1: 修改 `backend/settlement/service/game_settle_service.go`，GameSettleService 结构新增 robotChecker 和 virtualBalance 字段
  - [x] SubTask 27.2: 修改 NewGameSettleService 构造函数，注入 robotChecker 和 virtualBalance
  - [x] SubTask 27.3: 修改 executeSessionCredit 方法，开头新增机器人分支：if robotChecker.IsRobot → virtualBalance.Credit + BillRecord IsRobot=true + UpdateBillSuccess + return
  - [x] SubTask 27.4: 修改 settlePlayer 方法，开头新增机器人分支：if robotChecker.IsRobot → UpdateGameSettleStatusByUser + return（跳过 platform.Settle）
  - [x] SubTask 27.5: 确保非机器人走原流程不变

- [x] Task 28: SettlementService 新增虚拟通道
  - [x] SubTask 28.1: 修改 `backend/settlement/service/settlement_service.go`，SettlementService 结构新增 robotChecker 和 virtualBalance 字段
  - [x] SubTask 28.2: 修改 NewSettlementService 构造函数，注入 robotChecker 和 virtualBalance
  - [x] SubTask 28.3: 修改 CheckBalance 方法，开头新增机器人分支：if robotChecker.IsRobot → virtualBalance.GetBalance + 返回余额及 sufficient 判断
  - [x] SubTask 28.4: 修改 GetUserBalance 方法，开头新增机器人分支：if robotChecker.IsRobot → virtualBalance.GetBalance
  - [x] SubTask 28.5: 修改 creditRound 方法，创建 BillRecord 时根据 robotChecker.IsRobot 设置 IsRobot 字段
  - [x] SubTask 28.6: 确保非机器人走原流程不变

## 阶段九：Lua 脚本与身份标识

- [x] Task 29: 修改选座/加入房间 Lua 脚本传递 is_robot
  - [x] SubTask 29.1: 修改 `backend/game/infrastructure/persistence/redis/lua_scripts.go`，LuaSelectSeat 脚本新增 is_robot 参数
  - [x] SubTask 29.2: 修改 LuaJoinAsSpectator 脚本（如需要）传递 is_robot
  - [x] SubTask 29.3: 修改 `backend/game/infrastructure/persistence/redis/repository.go` 中 SelectSeat/JoinAsSpectator 方法调用，传递 is_robot 参数
  - [x] SubTask 29.4: 确保从 User.IsRobot 读取标识并传递给 Lua 脚本
  - [x] SubTask 29.5: 修改 RoomStateData 中 Player 结构，确保 is_robot 字段正确序列化/反序列化

## 阶段十：Bootstrap 装配与启动

- [x] Task 30: 修改 bootstrap/container.go 装配机器人服务
  - [x] SubTask 30.1: Container 结构新增字段：RobotAccountService, RobotSchedulerService, RobotPlayer, RobotBehaviorEngine, VirtualBalanceService（game 层）, RobotChecker, VirtualBalanceService（settlement 层）
  - [x] SubTask 30.2: InitAppServices 中创建 VirtualBalanceService（game 层）
  - [x] SubTask 30.3: InitAppServices 中创建 RobotAccountService
  - [x] SubTask 30.4: InitAppServices 中创建 RobotPlayer
  - [x] SubTask 30.5: InitAppServices 中创建 RobotBehaviorEngine
  - [x] SubTask 30.6: InitAppServices 中创建 RobotSchedulerService
  - [x] SubTask 30.7: 注册 TimeoutTypeRobot handler：`scheduler.RegisterHandler(scheduler.TimeoutTypeRobot, behaviorEngine.HandleRobotTimeout)`
  - [x] SubTask 30.8: 创建 VirtualBalanceSyncScheduler 并加入 StartSchedulers/Stop

- [x] Task 31: 修改 bootstrap/app.go 装配结算层虚拟通道
  - [x] SubTask 31.1: 创建 RobotChecker 实例
  - [x] SubTask 31.2: 创建结算层 VirtualBalanceService 实例（或复用 game 层）
  - [x] SubTask 31.3: 修改 NewDeductService 调用，注入 robotChecker 和 virtualBalance
  - [x] SubTask 31.4: 修改 NewGameSettleService 调用，注入 robotChecker 和 virtualBalance
  - [x] SubTask 31.5: 修改 NewSettlementService 调用，注入 robotChecker 和 virtualBalance

- [x] Task 32: 修改 GameEventConsumer 装配
  - [x] SubTask 32.1: 修改 NewGameEventConsumer，注入 RobotBehaviorEngine
  - [x] SubTask 32.2: 修改 bootstrap/container.go 中 NewGameEventConsumer 调用

- [x] Task 33: 启动时配置校验与机器人系统启动
  - [x] SubTask 33.1: 启动时调用 ValidateRobotConfig 校验配置
  - [x] SubTask 33.2: 若 robot.enabled = true，启动 RobotSchedulerService 和 VirtualBalanceSyncScheduler
  - [x] SubTask 33.3: 若 robot.enabled = false，跳过机器人系统启动

## 阶段十一：验证与测试

- [x] Task 34: 编译验证
  - [x] SubTask 34.1: 执行 `cd backend && go build ./...` 确保编译通过
  - [x] SubTask 34.2: 执行 `cd backend && go vet ./...` 确保无静态检查错误

- [x] Task 35: 单元测试（关键模块）
  - [x] SubTask 35.1: VirtualBalanceService 单元测试（Deduct/Credit/GetBalance/SyncToDB）
  - [x] SubTask 35.2: RobotChecker 单元测试（IsRobot）
  - [x] SubTask 35.3: RobotAccountService 单元测试（BatchCreateRobots/GetAvailableRobot）
  - [x] SubTask 35.4: 配置校验单元测试（ValidateRobotConfig 各校验规则）

# Task Dependencies

- Task 2 (RobotAccount 模型) 依赖 Task 1 (User IsRobot 字段)
- Task 3 (RobotAccount 仓库) 依赖 Task 2
- Task 6 (虚拟余额 Redis) 依赖 Task 2
- Task 7 (账号池 Redis) 依赖 Task 6
- Task 12 (RobotAccountService) 依赖 Task 3, 6, 7
- Task 15 (RobotPlayer) 依赖 Task 12, 16
- Task 18 (行为引擎) 依赖 Task 15, 17
- Task 21 (调度器) 依赖 Task 12, 15, 18
- Task 24 (RobotChecker) 依赖 Task 6
- Task 25 (结算层 VirtualBalanceService) 依赖 Task 6
- Task 26, 27, 28 (结算虚拟通道) 依赖 Task 24, 25
- Task 29 (Lua 脚本) 依赖 Task 1, 5
- Task 30, 31, 32 (Bootstrap 装配) 依赖所有前置任务
- Task 33 (启动校验) 依赖 Task 30, 31
- Task 34, 35 (验证测试) 依赖所有任务完成

# Parallelizable Work

以下任务可并行执行：
- Task 4 (BillRecord IsRobot) 与 Task 5 (Player IsRobot) 可并行
- Task 9 (配置结构) 与 Task 10 (game.yaml) 可并行
- Task 6, 7, 8（三个 Redis 服务）可并行
- Task 24 (RobotChecker) 与 Task 25 (结算层 VirtualBalanceService) 可并行
- Task 26, 27, 28（三个结算虚拟通道修改）可并行（但都依赖 Task 24, 25）
