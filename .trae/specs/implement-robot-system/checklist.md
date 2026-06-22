# Checklist

## 数据模型与基础设施
- [x] User 模型新增 IsRobot 字段（gorm:"default:false;index"），AutoMigrate 能自动添加
- [x] RobotAccount 数据模型定义完整（ID, UserID, Status, VirtualBalance, TotalVirtualDebit, TotalVirtualCredit, MinRoomFee, MaxRoomFee, TotalGames, TotalProfit, LastActiveAt, CreatedAt, UpdatedAt）
- [x] RobotAccount 状态常量定义正确（0=未激活 1=空闲 2=游戏中 3=停用）
- [x] RobotAccount 数据库仓库实现 CRUD + GetAvailableRobots（按费用等级过滤 + 余额升序）+ BatchCreate
- [x] BillRecord 模型新增 IsRobot 字段（gorm:"default:false;index"）
- [x] Player 结构新增 IsRobot 标记
- [x] PlayerInfo/SeatInfo 结构新增 IsRobot 字段（如需广播）

## Redis 存储与虚拟余额
- [x] 虚拟余额 Redis key 定义正确（robot:virtual_balance:{userID}、robot:virtual_balance:dirty、robot:user_ids）
- [x] VirtualBalanceService.Deduct 使用 INCRBY 原子扣减 + SADD dirty + 余额为负检查
- [x] VirtualBalanceService.Credit 使用 INCRBY 原子增加 + SADD dirty
- [x] VirtualBalanceService.GetBalance 缓存未命中时从 DB 加载
- [x] VirtualBalanceService.SyncToDB 正确同步脏数据到 DB 并清空 dirty SET
- [x] 机器人账号池 Redis 操作实现（AddToAvailablePool/RemoveFromAvailablePool/GetAvailableCount）
- [x] 调度器状态 Redis 操作实现（房间-机器人映射、分配锁、房间限流锁、回收冷却、活跃集合）

## 配置管理
- [x] Config 结构新增 Robot 字段（RobotConfig）
- [x] RobotConfig 包含 Scheduler/Behavior/Account 子结构，字段类型与文档一致（time.Duration 等）
- [x] game.yaml 新增 robot 段，默认值与文档 4.1 章节一致
- [x] 配置校验实现所有规则（MaxRobotsPerRoom+MinRealPlayers<=5、ReserveCount 比例、DelayMin<DelayMax、SendDelayMax<30s、GrabSkipProb [0,1]、InitialBalanceMulti>=1.0）
- [x] 配置校验不合法时自动修正或告警，不阻断启动（除自动修正项外）

## 机器人账号管理
- [x] RobotAccountService.BatchCreateRobots 正确创建 User（is_robot=true）+ RobotAccount + Redis 虚拟余额 + 账号池
- [x] 机器人 UserID 为合成值（robot_<seq>）
- [x] 机器人昵称/头像从名称库随机生成，存储在 User 表
- [x] GetAvailableRobot 按房间费用等级匹配 + 余额最低优先
- [x] MarkRobotInGame/MarkRobotIdle 正确更新状态和账号池
- [x] CheckLowBalance 虚拟余额低于阈值时停用机器人
- [x] 虚拟余额同步调度器按 sync_interval 定时调用 SyncToDB
- [x] init_robot_accounts.go 脚本可批量创建机器人

## 机器人玩家 (RobotPlayer)
- [x] JoinAndReady 入房为旁观者 + 检查空座 + 调度延迟选座
- [x] SelectSeat 查询空座 + 随机选择 + 调用 SeatAppService.SelectSeat + 链式调度延迟准备
- [x] Ready 调用 SeatAppService.PlayerReady
- [x] GrabPacket 调用 GrabService.GetAvailablePacketID + GameAppService.GrabPacket
- [x] SendPacket 调用 GameAppService.SendPacket
- [x] LeaveRoom 调用 SeatAppService.CancelSeat + RoomAppService.LeaveRoom
- [x] GrabService.GetAvailablePacketID 从 Redis 查询可用红包 ID

## 机器人行为引擎
- [x] TimeoutTypeRobot 常量定义（="robot"）
- [x] RobotBehaviorEngine.scheduleRobotAction 注册 TimeoutTypeRobot 延迟任务，data 格式 `robotUserID:action:uuid`
- [x] HandleRobotTimeout 正确解析 data 并分发到 RobotPlayer 对应方法（seat/ready/grab/send/leave）
- [x] 选座行为：随机延迟 seat_delay_min~seat_delay_max
- [x] 准备行为：选座成功后链式调度，延迟 ready_delay_min~ready_delay_max
- [x] 抢红包行为：grab_skip_prob 概率跳过，否则延迟 grab_delay_min~grab_delay_max
- [x] 发红包行为：延迟 send_delay_min~send_delay_max，必须 < 30s SendTimeout
- [x] 离场行为：延迟 leave_after_game_min~leave_after_game_max
- [x] GameEventConsumer 消费 packet_created/round_settle/session_end 事件时触发行为引擎

## 机器人调度器
- [x] 巡检主循环按 scan_interval 定时执行
- [x] 补位触发条件正确（RoomStatusWaiting + 已准备真人>=MinRealPlayers + 已选座<MaxPlayers）
- [x] 机器人分配数量按占座数计算（MaxPlayers - 已选座总人数）
- [x] 房间优先级排序（已准备真人数降序 + 等待时长升序）
- [x] 分配防抖：分配锁（robot:assign:{userID}）+ 房间限流锁（robot:room_assign:{roomID}）+ 回收冷却
- [x] 账号池余量保护（ReserveCount），低于时告警
- [x] 单次巡检时间预算控制（ScanInterval 的 80%）
- [x] OnGameEnd 事件驱动回收（查询房间机器人 → 取消调度 → 延迟离场 → 更新状态 → 归还账号池）
- [x] 定时巡检兜底回收已结束房间残留机器人
- [x] endGameWithOptions 触发 RobotSchedulerService.OnGameEnd

## 结算层虚拟通道
- [x] RobotChecker 接口定义（IsRobot(ctx, userID int64) bool）
- [x] redisRobotChecker 使用 SISMEMBER robot:user_ids 实现 O(1) 查询
- [x] 结算层 VirtualBalanceService 实现 Deduct/Credit/GetBalance
- [x] DeductService.executeSingleDeduct 机器人分支：virtualBalance.Deduct + BillRecord IsRobot=true + UpdateBillSuccess + 跳过 platform.Debit
- [x] GameSettleService.executeSessionCredit 机器人分支：virtualBalance.Credit + BillRecord IsRobot=true + UpdateBillSuccess + 跳过 platform.Credit
- [x] GameSettleService.settlePlayer 机器人分支：UpdateGameSettleStatusByUser + 跳过 platform.Settle
- [x] SettlementService.CheckBalance 机器人分支：virtualBalance.GetBalance + 返回余额及 sufficient 判断 + 跳过 platform.GetBalance
- [x] SettlementService.GetUserBalance 机器人分支：virtualBalance.GetBalance + 跳过 platform.GetBalance
- [x] SettlementService.creditRound 创建 BillRecord 时根据 robotChecker.IsRobot 设置 IsRobot 字段
- [x] 非机器人走原流程不变（真人资金操作零影响）

## Lua 脚本与身份标识
- [x] LuaSelectSeat 脚本新增 is_robot 参数
- [x] LuaJoinAsSpectator 脚本（如需要）传递 is_robot
- [x] repository.go 中 SelectSeat/JoinAsSpectator 方法调用传递 is_robot 参数
- [x] 从 User.IsRobot 读取标识并传递给 Lua 脚本
- [x] RoomStateData 中 Player 结构 is_robot 字段正确序列化/反序列化

## Bootstrap 装配与启动
- [x] Container 结构包含所有机器人相关服务字段
- [x] InitAppServices 正确创建所有机器人服务实例
- [x] 注册 TimeoutTypeRobot handler 到 TimeoutScheduler
- [x] VirtualBalanceSyncScheduler 加入 StartSchedulers/Stop
- [x] bootstrap/app.go 创建 RobotChecker 和结算层 VirtualBalanceService
- [x] NewDeductService/NewGameSettleService/NewSettlementService 注入 robotChecker 和 virtualBalance
- [x] GameEventConsumer 注入 RobotBehaviorEngine
- [x] 启动时调用 ValidateRobotConfig 校验配置
- [x] robot.enabled=true 时启动调度器，false 时不启动

## 验证与测试
- [x] `cd backend && go build ./...` 编译通过
- [x] `cd backend && go vet ./...` 无静态检查错误
- [x] VirtualBalanceService 单元测试通过
- [x] RobotChecker 单元测试通过
- [x] RobotAccountService 单元测试通过
- [x] 配置校验单元测试通过

## 业务规则验证
- [x] 机器人补位后游戏能快速开始（5人满员触发 3 秒倒计时开局）
- [x] 机器人行为模拟真人节奏（随机延迟，不可通过行为模式识别）
- [x] 机器人扣款/入账/结算走虚拟通道，零平台 API 调用
- [x] 真人玩家资金流程完全不变
- [x] 机器人虚拟余额不足时不参与该等级房间
- [x] 机器人游戏结束后自动离场，回到账号池
- [x] 机器人选座前检查空座，无空座返回 ErrNoEmptySeat 并归还账号池
- [x] 配置支持运行时热更新（通过 Nacos）
