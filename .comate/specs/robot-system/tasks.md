# 机器人系统实现任务计划

- [ ] Task 1: 机器人账号数据模型与数据库层
    - 1.1: 创建 RobotAccount 数据模型，包含虚拟余额字段 (`backend/game/model/robot_account.go`)
    - 1.2: 创建 robot_accounts 表的数据库迁移脚本
    - 1.3: 实现 RobotAccountRepository MySQL 读写 (`backend/game/infrastructure/persistence/mysql/robot_account_repo.go`)

- [ ] Task 2: 机器人账号池 Redis 管理
    - 2.1: 实现空闲账号池 Redis 数据结构（按房间等级分桶的 Sorted Set）
    - 2.2: 实现游戏中账号集合管理
    - 2.3: 实现账号池操作方法（获取空闲机器人/归还/状态更新/按等级查询）
    - 2.4: 维护 robot:user_ids SET（供 RobotChecker 使用）
    - 2.5: 实现账号池初始化（从 MySQL 加载到 Redis）

- [ ] Task 3: 虚拟余额服务实现
    - 3.1: 实现虚拟余额 Redis 操作 (`backend/game/infrastructure/persistence/redis/virtual_balance.go`)：INCRBY/GET/SET
    - 3.2: 实现 VirtualBalanceService 核心逻辑 (`backend/game/application/virtual_balance_service.go`)：Deduct/Credit/GetBalance
    - 3.3: 实现脏数据追踪（SADD dirty set）
    - 3.4: 实现定时同步调度器 (`backend/game/scheduler/virtual_balance_sync.go`)：Redis → DB 批量同步
    - 3.5: 实现启动加载：DB → Redis 余额初始化
    - 3.6: 实现缓存未命中时的 DB 回源加载

- [ ] Task 4: 机器人账号管理 Service
    - 4.1: 实现 RobotAccountService 核心业务逻辑 (`backend/game/application/robot_account_service.go`)
    - 4.2: 实现批量创建机器人账号（生成 User + RobotAccount，随机昵称/头像）
    - 4.3: 实现账号激活/停用/状态管理
    - 4.4: 实现虚拟余额检查（余额是否满足指定档位房间要求）
    - 4.5: 实现低余额下线（虚拟余额 < 阈值时自动停用，从账号池移除）

- [ ] Task 5: 机器人账号初始化脚本
    - 5.1: 实现批量初始化脚本 (`backend/scripts/init_robot_accounts.go`)
    - 5.2: 脚本读取配置文件中的机器人账号信息，创建 User + RobotAccount
    - 5.3: 创建完成后加载到 Redis 账号池，设定初始虚拟余额

- [ ] Task 6: 机器人玩家标识扩展
    - 6.1: Player 结构新增 IsRobot 标记 (`backend/game/domain/room.go`)
    - 6.2: Redis 房间状态中存储 is_robot 标记
    - 6.3: 选座/加入房间 Lua 脚本适配（传递 is_robot 标记）(`backend/game/infrastructure/persistence/redis/lua_scripts.go`)
    - 6.4: BillRecord 模型新增 IsRobot 字段 (`backend/settlement/model/bill.go`) + 数据库迁移

- [ ] Task 7: RobotChecker 机器人身份识别
    - 7.1: 定义 RobotChecker 接口 (`backend/settlement/service/robot_checker.go`)
    - 7.2: 实现 redisRobotChecker（基于 robot:user_ids SET，SISMEMBER O(1) 查询）
    - 7.3: 在 Settlement Service 依赖注入中注册 RobotChecker

- [ ] Task 8: Settlement 虚拟通道 — 扣款虚拟通道
    - 8.1: 修改 `DeductService.executeSingleDeduct`：新增机器人判断分支，机器人走虚拟扣款（VirtualBalance.Deduct + BillRecord 直接 Success），跳过 platform.Debit
    - 8.2: 创建 BillRecord 时设置 IsRobot=true（扣款场景）

- [ ] Task 9: Settlement 虚拟通道 — 入账与结算虚拟通道
    - 9.1: 修改 `GameSettleService.executeSessionCredit`：新增机器人判断分支，机器人走虚拟入账（VirtualBalance.Credit + BillRecord 直接 Success），跳过 platform.Credit
    - 9.2: 修改 `GameSettleService.settlePlayer`：新增机器人判断分支，机器人跳过 platform.Settle，仅更新 BillGameSettleStatus
    - 9.3: 创建 BillRecord 时设置 IsRobot=true（入账场景）
    - 9.4: 修改 `SettlementService.creditRound`：创建 BillRecord 时设置 IsRobot=true（抢红包收入场景）

- [ ] Task 10: Settlement 虚拟通道 — 余额查询虚拟通道
    - 10.1: 修改 `SettlementService.CheckBalance`：机器人走虚拟余额查询（VirtualBalance.GetBalance），跳过 platform.GetBalance
    - 10.2: 修改 `SettlementService.GetUserBalance`：机器人走虚拟余额查询，跳过 platform.GetBalance

- [ ] Task 11: RobotPlayer 核心实现
    - 11.1: 实现 RobotPlayer 结构体及依赖注入 (`backend/game/application/robot_player.go`)
    - 11.2: 实现 JoinAndReady 方法（加入房间为旁观者 → 选座 → 准备）
    - 11.3: 实现 SelectSeat 方法（查询空座 + 调用 SeatAppService.SelectSeat）
    - 11.4: 实现 Ready 方法（调用 SeatAppService.PlayerReady）
    - 11.5: 实现 GrabPacket 方法（查询可用红包 + 调用 GameAppService.GrabPacket）
    - 11.6: 实现 SendPacket 方法（调用 GameAppService.SendPacket）
    - 11.7: 实现 LeaveRoom 方法（取消座位 + 离开房间）
    - 11.8: 实现 pickRandomEmptySeat 辅助方法

- [ ] Task 12: GrabService 扩展
    - 12.1: 新增 GetAvailablePacketID 方法 (`backend/game/application/grab_service.go`)，供机器人查询可抢的红包 ID

- [ ] Task 13: 机器人行为引擎核心框架
    - 13.1: 定义 RobotBehaviorConfig 配置结构 (`backend/game/domain/robot_behavior.go`)，包含各行为延迟范围和概率参数
    - 13.2: 实现 RobotBehaviorEngine 核心逻辑（随机延迟计算、概率决策、事件分发）
    - 13.3: 配置文件新增机器人行为参数 (`backend/config/`)
    - 13.4: 实现行为引擎启动/停止生命周期管理

- [ ] Task 14: 行为引擎与 TimeoutScheduler 集成
    - 14.1: 新增 TimeoutTypeRobot 类型 (`backend/game/scheduler/timeout_scheduler.go`)
    - 14.2: 实现 scheduleRobotAction 方法（注册延迟行为到 Redis ZSET）
    - 14.3: 实现 HandleRobotTimeout 方法（解析 data 字段，分发到对应行为方法）
    - 14.4: 在 bootstrap 中注册 TimeoutTypeRobot Handler (`backend/game/bootstrap/container.go`)

- [ ] Task 15: 机器人入座/准备行为实现
    - 15.1: 实现调度器分配后触发选座行为（随机延迟 2-5s + SelectSeat）
    - 15.2: 实现选座后触发准备行为（随机延迟 1-3s + Ready）

- [ ] Task 16: 机器人抢红包行为实现
    - 16.1: 监听 Kafka `packet_created` 事件，识别有机器人的房间
    - 16.2: 实现 GrabSkipProb 概率跳过逻辑（5% 概率不抢）
    - 16.3: 实现延迟抢红包（随机延迟 1-8s + GrabPacket）
    - 16.4: 在 `game_event_consumer.go` 中集成行为引擎触发

- [ ] Task 17: 机器人发红包行为实现
    - 17.1: 监听 Kafka `round_settle` 事件，识别最小金额获得者是否为机器人
    - 17.2: 实现延迟发红包（随机延迟 2-5s + SendPacket），确保在 30s 超时前完成

- [ ] Task 18: 机器人游戏结束离场行为实现
    - 18.1: 监听 Kafka `session_end` 事件
    - 18.2: 实现延迟离场（随机延迟 3-10s + LeaveRoom）
    - 18.3: 离场时更新 RobotAccount 状态为空闲，归还账号池

- [ ] Task 19: 机器人调度器核心框架
    - 19.1: 实现 RobotSchedulerService 定时巡检主循环 (`backend/game/application/robot_scheduler_service.go`)
    - 19.2: 实现调度规则：补位触发条件（Waiting + 真人数 >= 2 + 人数不足）
    - 19.3: 实现调度规则：房间优先级排序（已准备真人多的房间优先）
    - 19.4: 实现调度规则：机器人选择策略（余额匹配 + 余额最低优先）
    - 19.5: 实现调度规则：分配防抖（分布式锁 + 房间限流锁 + 回收冷却 + 余量保护）
    - 19.6: 实现调度配置 (RobotSchedulerConfig)

- [ ] Task 20: 机器人调度状态管理与回收
    - 20.1: 实现调度状态 Redis 存储 (`backend/game/infrastructure/persistence/redis/robot_scheduler.go`)：机器人-房间映射
    - 20.2: 实现游戏结束后机器人回收（事件驱动：监听 session_end + 定时巡检兜底）
    - 20.3: 实现回收时虚拟余额同步到 DB
    - 20.4: 实现服务重启恢复（从 Redis 恢复机器人-房间映射和调度状态）
    - 20.5: 修改 `game_app_service.go` 的 `endGameWithOptions` 触发机器人回收通知

- [ ] Task 21: 虚拟余额同步与低余额下线
    - 21.1: 实现定时同步调度器（每 30s 将 Redis 脏数据同步到 DB）
    - 21.2: 实现低余额自动停用（虚拟余额 < 最低房间费用时自动停用，从账号池移除）

- [ ] Task 22: 集成测试
    - 22.1: 机器人完整流程联调测试（调度器分配 → 入座准备 → 抢红包 → 发红包 → 虚拟结算 → 游戏结束离场）
    - 22.2: 边界条件测试（虚拟余额不足/发红包超时/Redis 宕机/并发分配/全机器人房间限制）
    - 22.3: 服务重启恢复测试
