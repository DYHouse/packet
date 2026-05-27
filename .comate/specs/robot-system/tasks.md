# 机器人系统实现任务计划

- [ ] Task 1: 机器人账号数据模型与数据库层
    - 1.1: 创建 RobotAccount 数据模型 (`backend/game/model/robot_account.go`)
    - 1.2: 创建 robot_accounts 表的数据库迁移脚本
    - 1.3: 实现 RobotAccountRepository MySQL 读写 (`backend/game/infrastructure/persistence/mysql/robot_account_repo.go`)

- [ ] Task 2: 机器人账号池 Redis 管理
    - 2.1: 实现空闲账号池 Redis 数据结构 (按房间等级分桶的 Sorted Set)
    - 2.2: 实现游戏中账号集合管理
    - 2.3: 实现账号池操作方法 (获取空闲机器人/归还/状态更新/按等级查询)
    - 2.4: 实现账号池初始化 (从 MySQL 加载到 Redis)

- [ ] Task 3: 机器人账号管理 Service
    - 3.1: 实现 RobotAccountService 核心业务逻辑 (`backend/game/application/robot_account_service.go`)
    - 3.2: 实现批量创建机器人账号 (生成 User + RobotAccount, 随机昵称/头像)
    - 3.3: 实现账号激活/停用/状态管理
    - 3.4: 实现余额检查 (与 BalanceService.CheckBalanceForReady 对接)
    - 3.5: 实现余额同步方法 (调用 platform.GetBalance 更新本地缓存)
    - 3.6: 实现低余额下线 (余额不足时自动停用，从账号池移除)

- [ ] Task 4: 机器人账号初始化脚本
    - 4.1: 实现批量初始化脚本 (`backend/scripts/init_robot_accounts.go`)
    - 4.2: 脚本读取配置文件中的机器人账号信息，创建 User + RobotAccount
    - 4.3: 创建完成后加载到 Redis 账号池

- [ ] Task 5: 机器人玩家标识扩展
    - 5.1: Player 结构新增 IsRobot 标记 (`backend/game/domain/room.go`)
    - 5.2: Redis 房间状态中存储 is_robot 标记
    - 5.3: 选座/加入房间 Lua 脚本适配 (传递 is_robot 标记) (`backend/game/infrastructure/persistence/redis/lua_scripts.go`)
    - 5.4: BillRecord 模型新增 IsRobot 字段 (`backend/settlement/model/bill_record.go`)

- [ ] Task 6: 机器人行为引擎核心框架
    - 6.1: 定义 RobotBehaviorConfig 配置结构 (`backend/game/domain/robot_behavior.go`)
    - 6.2: 实现 RobotBehaviorEngine 核心逻辑 (随机延迟计算/概率决策)
    - 6.3: 实现 RobotBehaviorScheduler 定时调度器 (`backend/game/scheduler/robot_behavior_scheduler.go`)
    - 6.4: 配置文件新增机器人行为参数 (`backend/config/`)
    - 6.5: 实现行为引擎启动/停止生命周期管理

- [ ] Task 7: 机器人入座/准备行为实现
    - 7.1: 实现机器人选座行为 (随机延迟 + 调用 SeatAppService.SelectSeat)
    - 7.2: 实现机器人准备行为 (延迟后调用 SeatAppService.PlayerReady)
    - 7.3: 对接 SeatAppService 支持内部调用入口 (`backend/game/application/seat_app_service.go`)

- [ ] Task 8: 机器人抢红包行为实现
    - 8.1: 实现抢红包延迟决策 (1-8秒随机延迟)
    - 8.2: 实现概率跳过逻辑 (GrabSkipProb 概率不抢)
    - 8.3: 监听游戏阶段事件, 在 Grabbing 阶段触发抢红包
    - 8.4: 对接 GameAppService 支持内部抢红包调用 (`backend/game/application/game_app_service.go`)

- [ ] Task 9: 机器人发红包行为实现
    - 9.1: 监听结算事件，识别机器人是否为最小金额获得者（即下一轮发送者）
    - 9.2: 监听 WaitSend 阶段，若机器人为发送者则延迟 2-5 秒后发红包
    - 9.3: 对接 GameAppService 支持内部发红包调用
    - 9.4: 确保发红包在 30 秒超时前完成

- [ ] Task 10: 机器人游戏结束离场行为实现
    - 10.1: 监听 PhaseGameEnd 事件
    - 10.2: 延迟 3-10 秒后执行离场操作
    - 10.3: 离场时更新 RobotAccount 状态为空闲, 归还账号池

- [ ] Task 11: 机器人调度器核心框架
    - 11.1: 实现 RobotSchedulerService 定时巡检主循环 (`backend/game/application/robot_scheduler_service.go`)
    - 11.2: 实现调度状态 Redis 存储 (`backend/game/infrastructure/persistence/redis/robot_scheduler.go`)
    - 11.3: 机器人分配使用 Redis 分布式锁 (防止同一机器人分配到多个房间)

- [ ] Task 12: 机器人分配策略实现
    - 12.1: 根据房间费用等级匹配机器人账号 (MinRoomFee/MaxRoomFee 检查)
    - 12.2: 余额检查 (调用 RobotAccountService 余额校验)
    - 12.3: 单房间最大机器人数限制
    - 12.4: 至少 1 个真人才允许开局检查

- [ ] Task 13: 机器人回收与余额同步
    - 13.1: GameEnd 事件通知调度器回收机器人 (`backend/game/application/game_app_service.go`)
    - 13.2: 游戏结束后调用离场逻辑 (CancelSeat + LeaveRoom)
    - 13.3: 调用 platform.GetBalance() 同步机器人余额到本地缓存
    - 13.4: 余额不足时标记停用, 从账号池移除
    - 13.5: 调度状态 Redis 持久化 (机器人-房间映射)
    - 13.6: 服务重启时从 Redis 恢复机器人状态

- [ ] Task 14: 管理 API 实现
    - 14.1: 实现 RobotAdminHandler HTTP 接口 (`backend/gateway/handler/robot_admin_handler.go`)
    - 14.2: 实现 RobotAdminService 管理业务逻辑 (`backend/gateway/service/robot_admin_service.go`)
    - 14.3: 注册管理路由 (`backend/gateway/router.go`)
    - 14.4: 实现: 批量创建机器人账号 API
    - 14.5: 实现: 查询机器人账号列表 API
    - 14.6: 实现: 启停机器人账号 API
    - 14.7: 实现: 实时监控数据 API

- [ ] Task 15: 集成测试
    - 15.1: 机器人完整流程联调测试 (巡检触发 → 补位 → 抢红包 → 发红包 → 结算 → 离场)
    - 15.2: 边界条件测试 (余额不足/发红包超时/全机器人房间限制/并发分配)
    - 15.3: 服务重启恢复测试
