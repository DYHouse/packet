# 机器人系统方案设计文档 (Robot System Design Document)

## 1. 需求场景与处理逻辑

### 1.1 业务痛点
游戏刚开始推广，玩家上座率不高，导致真人玩家在房间中长时间等待，影响游戏体验和留存率。需要一个机器人系统模拟真人玩家补位，提高房间上座率和游戏启动效率。

### 1.2 核心目标
- 当房间真人不足时，自动分配机器人补位，使游戏能够快速开始
- 机器人行为需模拟真人，包括入座、准备、抢红包、发红包等操作，带有随机延迟
- 机器人对应平台账号，平台侧预先充值，扣款和结算与真人完全一致
- 支持通过管理 API 管理机器人账号（暂不开发后台页面，暂不做策略配置）

### 1.3 处理逻辑
1. 真人玩家进入房间、选座、点击准备
2. 定时巡检扫描 Waiting 房间，对有已准备真人但人数不足开局的房间分配机器人补位
3. 机器人通过 Game Service 内部接口执行：进入房间 → 选座 → 准备 → 抢红包/发红包等操作
4. 游戏过程中机器人行为模拟真人节奏（带随机延迟），包括抢红包和发红包
5. 游戏结束后机器人自动离场，回到账号池等待下次分配
6. 真人作为旁观者进入房间，等当前局游戏结束后机器人自动离场，真人选空座入局

---

## 2. 系统架构与技术方案

### 2.1 整体架构

```
┌──────────────────────────────────────────────────────┐
│                  管理 API (HTTP)                      │
│  机器人账号管理 | 监控数据                              │
└────────────────────┬─────────────────────────────────┘
                     │ HTTP API
┌────────────────────▼─────────────────────────────────┐
│                   Gateway Service                     │
│  RobotAdminHandler (管理接口)                          │
│  - 机器人账号CRUD | 监控数据                            │
└────────────────────┬─────────────────────────────────┘
                     │ gRPC
┌────────────────────▼─────────────────────────────────┐
│                   Game Service                        │
│  RobotScheduler (机器人调度器)                         │
│  - 定时巡检 | 机器人分配/回收                           │
│  RobotBehaviorEngine (行为引擎)                        │
│  - 延迟决策 | 行为模拟                                 │
│  RobotPlayer (机器人玩家)                              │
│  - 内部调用选座/准备/抢红包/发红包，不走WebSocket        │
└────────────────────┬─────────────────────────────────┘
                     │ Kafka
┌────────────────────▼─────────────────────────────────┐
│                Settlement Service                     │
│  机器人结算与真人完全一致                                │
│  正常走 platform.Debit/Credit/Settle                  │
└──────────────────────────────────────────────────────┘
```

### 2.2 技术选型
- **机器人连接方式**: 在 Game Service 内部实现 `RobotPlayer`，不经过 Gateway WS，直接调用 Game Service 内部方法（选座、准备、抢红包、发红包等），大幅降低复杂度
- **行为模拟**: 基于定时器 + 随机延迟的决策引擎，模拟真人操作节奏
- **调度策略**: 定时巡检驱动，周期扫描 Waiting 房间，按需分配机器人
- **结算处理**: 机器人拥有平台账号，平台侧预先充值，扣款和结算走正常平台 API 流程，与真人完全一致，无需特殊适配
- **账号管理**: 机器人账号预创建并存储在数据库，预充值由平台侧操作，本系统不负责充值
- **管理方式**: 通过 HTTP API 提供管理能力（暂不开发后台页面）
- **离场策略**: 游戏结束后机器人统一自动离场，回到账号池等待下次调度分配，不做中途替换

---

## 3. 模块设计与功能点

### 3.1 模块一：机器人账号管理 (Robot Account Management)

#### 功能点
- **机器人账号批量创建**: 支持批量创建机器人账号，生成内部用户记录和平台用户ID映射
- **账号状态管理**: 激活/停用/余额不足标记，余额不足时自动下线
- **账号池管理**: 维护可用机器人账号池，按房间等级分配账号（确保机器人余额匹配房间费用要求）
- **账号信息模拟**: 随机昵称生成（本地化名称库）、随机头像分配，使机器人看起来像不同玩家

#### 数据模型
```go
// RobotAccount 机器人账号表
type RobotAccount struct {
    ID             int64     `gorm:"primaryKey"`
    UserID         int64     `gorm:"uniqueIndex"`         // 关联 users.id
    PlatformUserID string    `gorm:"uniqueIndex;size:64"` // 平台用户ID
    Nickname       string    `gorm:"size:100"`
    Avatar         string    `gorm:"size:512"`
    Status         int       // 0=未激活 1=空闲 2=游戏中 3=停用
    Balance        int64     // 当前余额(分), 缓存值, 通过platform.GetBalance()同步
    MinRoomFee     int       // 可参与的最低房间费用
    MaxRoomFee     int       // 可参与的最高房间费用
    TotalGames     int       // 总参与局数
    TotalProfit    int64     // 总盈亏(分)
    LastActiveAt   time.Time // 最后活跃时间
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

#### 影响文件
- **新建**: `backend/game/model/robot_account.go` - 数据模型
- **新建**: `backend/game/infrastructure/persistence/mysql/robot_account_repo.go` - 数据库操作
- **新建**: `backend/game/infrastructure/persistence/redis/robot_pool.go` - 账号池 Redis 缓存
- **新建**: `backend/game/application/robot_account_service.go` - 账号管理业务逻辑
- **新建**: `backend/scripts/init_robot_accounts.go` - 机器人账号初始化脚本

---

### 3.2 模块二：机器人调度系统 (Robot Scheduler)

#### 功能点
- **定时巡检**: 周期扫描所有 Waiting 房间，对有已准备真人但人数不足开局的房间分配机器人补位
- **分配策略**:
  - 根据房间费用等级匹配机器人账号（余额检查）
  - 同一房间最多分配 N 个机器人（可配置，避免全是机器人）
  - 至少需要 1 个真人才允许开局（可配置）
- **机器人回收**: 游戏结束后，机器人自动离场回到账号池

#### 调度流程
```
定时巡检 (可配置间隔, 如5秒):
  扫描所有 Waiting 房间
  → 有已准备真人 且 总人数 < 开局所需人数?
    → 是: 从RobotPool获取空闲机器人 → 检查余额 → 分配到房间
    → 否: 继续扫描下一个房间
```

#### 影响文件
- **新建**: `backend/game/application/robot_scheduler_service.go` - 调度核心逻辑
- **新建**: `backend/game/infrastructure/persistence/redis/robot_scheduler.go` - 调度状态存储
- **修改**: `backend/game/application/game_app_service.go` - GameEnd 事件触发机器人回收

---

### 3.3 模块三：机器人行为引擎 (Robot Behavior Engine)

#### 功能点
- **入座行为**: 模拟真人选座，带 2-5 秒随机延迟
- **准备行为**: 入座后 1-3 秒自动准备
- **抢红包行为**: 
  - 抢红包延迟：1-8 秒随机（模拟真人反应时间）
  - 不必每次都抢：小额房间可设置不抢概率（5%）
- **发红包行为**: 当机器人上一轮抢到最小金额时，下一轮成为发送者，需在 WaitSend 阶段内发红包
  - 发红包延迟：2-5 秒随机（模拟真人思考和操作时间）
  - 遵守现有超时规则：30 秒内未发送则系统代发并扣惩罚，机器人需在此前完成发送
- **超时处理**: 遵守现有游戏超时规则，无需特殊处理
- **游戏结束行为**: 游戏结束后 3-10 秒自动离开房间
- **行为可配置**: 延迟范围、概率参数均可通过配置文件调整
- **防检测**: 行为模式加入随机因子，避免规律性

#### 核心设计
```go
type RobotBehaviorEngine struct {
    config    RobotBehaviorConfig
    scheduler *RobotBehaviorScheduler
}

type RobotBehaviorConfig struct {
    SeatDelayMin      time.Duration // 选座延迟下限
    SeatDelayMax      time.Duration // 选座延迟上限
    ReadyDelayMin     time.Duration // 准备延迟下限
    ReadyDelayMax     time.Duration // 准备延迟上限
    GrabDelayMin      time.Duration // 抢红包延迟下限
    GrabDelayMax      time.Duration // 抢红包延迟上限
    GrabSkipProb      float64       // 不抢概率
    SendDelayMin      time.Duration // 发红包延迟下限
    SendDelayMax      time.Duration // 发红包延迟上限
    LeaveAfterGameMin time.Duration // 游戏后离场延迟下限
    LeaveAfterGameMax time.Duration // 游戏后离场延迟上限
}
```

#### 影响文件
- **新建**: `backend/game/domain/robot_behavior.go` - 行为引擎核心逻辑
- **新建**: `backend/game/scheduler/robot_behavior_scheduler.go` - 行为定时调度
- **修改**: `backend/game/application/seat_app_service.go` - 支持机器人选座/准备入口
- **修改**: `backend/game/application/game_app_service.go` - 支持机器人抢红包/发红包入口

---

### 3.4 模块四：机器人玩家标识与余额同步 (Robot Player Identity & Balance Sync)

> 机器人拥有平台账号，平台侧预先充值，扣款和结算与真人完全一致，走正常 `platform.Debit/Credit/Settle` 流程，**无需对 Settlement Service 做任何修改**。本模块仅需解决两个问题：1) 在游戏内标识机器人身份，用于调度和统计；2) 同步机器人余额到本地缓存，用于调度器判断是否可参与。

#### 功能点
- **Player 身份标识**: 在 Room/Player 结构中新增 `IsRobot` 标记，用于调度器识别机器人身份
- **BillRecord 标记**: 新增 `is_robot` 字段，用于运营侧区分机器人和真人账单，做独立的盈亏统计（不影响结算流程）
- **余额同步**: 游戏结束后通过 `platform.GetBalance()` 同步机器人余额到 `RobotAccount.Balance` 缓存字段，供调度器判断是否可继续参与
- **低余额下线**: 机器人余额低于可参与最低房间费用时，自动从账号池移除并标记停用（需平台侧充值后重新激活）

#### 影响文件
- **修改**: `backend/game/domain/room.go` - Player 结构新增 `IsRobot` 标记
- **修改**: `backend/game/infrastructure/persistence/redis/lua_scripts.go` - 选座/加入房间 Lua 脚本传递 is_robot 标记
- **修改**: `backend/settlement/model/bill_record.go` - 新增 `IsRobot` 字段（仅用于统计，不影响结算逻辑）
- **修改**: `backend/game/application/robot_account_service.go` - 新增余额同步方法
- **无修改**: `settlement/service/` 下的 deduct_service.go、settlement_service.go、game_settle_service.go 均无需修改

---

### 3.5 模块五：管理 API (Robot Admin API)

> 暂不开发后台页面，仅提供后端 HTTP API。初期不提供策略配置和充值接口，充值由平台侧操作。

#### 功能点
- **机器人账号 CRUD**: 创建、查看、启停机器人账号
- **实时监控数据**: 
  - 在线机器人数量/分布
  - 机器人盈亏统计

#### API 设计
```
POST   /admin/robot/accounts           批量创建机器人账号
GET    /admin/robot/accounts            查询机器人账号列表
PUT    /admin/robot/accounts/:id/status 启停机器人账号
GET    /admin/robot/monitor              实时监控数据
```

#### 影响文件
- **新建**: `backend/gateway/handler/robot_admin_handler.go` - HTTP Handler
- **新建**: `backend/gateway/service/robot_admin_service.go` - 管理业务逻辑
- **修改**: `backend/gateway/router.go` - 注册管理路由

---

## 4. 数据流路径

### 4.1 机器人补位完整流程
```
1. 真人进入房间 → 选座 → 点击准备 (Gateway WS → Game Service)
2. 定时巡检扫描到该房间有已准备真人但人数不足开局
3. RobotScheduler 从 RobotPool(Redis) 获取空闲机器人
4. 检查机器人余额是否满足房间要求
5. 调用 Game Service 内部方法执行: 入房 → 选座(延迟2-5s) → 准备(延迟1-3s)
6. 机器人选座 → 房间玩家数增加 → 广播给真人
7. 游戏开始后，行为引擎监听游戏事件：
   a. 抢红包阶段(Grabbing) → 延迟1-8s后自动抢
   b. 结算阶段(Settling) → 识别最小金额获得者
   c. 等待发送阶段(WaitSend) → 若机器人为发送者，延迟2-5s后发红包
   d. 结算正常走平台API（与真人一致）
8. 游戏结束(PhaseGameEnd) → 延迟3-10s后自动离场，回到账号池
9. 真人作为旁观者等待 → 游戏结束时机器人自动离场 → 真人选空座
```

### 4.2 机器人结算流程（与真人一致）
```
1. Round Deduct: 正常调用 platform.Debit() 扣款
2. Round Credit: 正常调用 platform.Credit() 入账
3. Session Settle: 正常调用 platform.Settle() 上报结果
4. 余额同步: 游戏结束后调用 platform.GetBalance() 更新机器人本地缓存余额
5. 统计标记: BillRecord 的 is_robot 字段用于运营侧独立统计机器人盈亏
```

---

## 5. 边界条件与异常处理

### 5.1 机器人余额不足
- 选座前检查余额是否满足 `totalRequired`（与真人一致，走正常 BalanceService.CheckBalanceForReady）
- 发红包前同样需检查余额（发送者需支付 roomFee）
- 余额不足时机器人不参与该等级房间，调度器跳过该账号
- 余额低于最低房间费用时自动停用，需平台侧充值后通过管理 API 重新激活

### 5.2 机器人掉线/服务重启
- Game Service 重启时，从 Redis 恢复机器人状态
- 机器人游戏中的状态持久化到 Redis（标记为 robot player）
- 重启后恢复游戏中的机器人行为调度

### 5.3 全机器人房间
- 单房间最大机器人数限制，防止全机器人房间
- 至少需要 1 个真人才允许开局（可配置）

### 5.4 并发安全
- 机器人分配使用 Redis 分布式锁，防止同一机器人被分配到多个房间
- Lua 脚本选座复用现有原子操作逻辑

### 5.5 平台 API 故障
- 余额查询失败：使用缓存余额，标记待同步
- 扣款/结算失败：走现有结算重试机制，与真人一致

### 5.6 机器人发红包超时
- 机器人必须在 WaitSend 超时（30秒）前完成发红包
- 若行为引擎因故障未能触发，走现有超时逻辑（系统代发 + 惩罚）
- 正常情况下机器人 2-5 秒内发送，远小于超时时间

---

## 6. 预期成果

### 6.1 功能成果
- 完整的机器人补位系统，支持自动补位、行为模拟
- 机器人能完整参与游戏流程：抢红包 + 发红包，与真人行为一致
- 机器人扣款/结算与真人完全一致，无特殊适配
- 管理 API 支持机器人账号管理和监控

### 6.2 性能指标
- 补位响应时间取决于巡检间隔，配合巡检周期（如5秒）可实现快速补位
- 单实例支持 500+ 机器人同时在线
- 机器人操作延迟模拟真实度 > 95%（不可通过行为模式识别）

### 6.3 可扩展性
- 行为引擎支持插件式扩展新的行为策略
- 后续可增加事件驱动触发（如真人准备后即时触发）提升响应速度
- 后续可增加策略配置中心和后台页面
- 机器人数量可水平扩展

---

## 7. 人天计划表

### 总计：约 18 人天

| 序号 | 模块 | 任务 | 人天 | 说明 |
|------|------|------|------|------|
| 1 | 机器人账号管理 | 数据模型设计与建表 | 0.5 | robot_accounts 表 + users 表关联 |
| 2 | 机器人账号管理 | 账号 CRUD Repository 实现 | 1 | MySQL 读写 + 查询条件 |
| 3 | 机器人账号管理 | 账号池 Redis 管理实现 | 1 | 空闲池/游戏中池/按等级分桶 |
| 4 | 机器人账号管理 | 账号管理 Service 实现 | 1.5 | 创建/激活/停用/余额检查/昵称生成 |
| 5 | 机器人账号管理 | 批量创建脚本 | 0.5 | 初始化脚本，账号创建+加载到Redis |
| 6 | 机器人调度系统 | 调度器核心框架（定时巡检） | 1.5 | 周期扫描 Waiting 房间 + 评估分配 |
| 7 | 机器人调度系统 | 分配策略（余额检查/数量控制） | 1.5 | 等级匹配/余额检查/最大机器人数限制 |
| 8 | 机器人调度系统 | 游戏结束后机器人回收 | 1 | 离场 + 余额同步 + 状态重置 + 回收至账号池 |
| 9 | 机器人调度系统 | 调度状态 Redis 持久化与恢复 | 1 | 机器人-房间映射/服务重启恢复 |
| 10 | 机器人行为引擎 | 行为引擎核心框架 | 2 | 延迟调度器 + 随机决策 + 事件驱动 |
| 11 | 机器人行为引擎 | 入座/准备行为实现 | 1 | 随机选座 + 延迟准备 |
| 12 | 机器人行为引擎 | 抢红包行为实现 | 1.5 | 延迟抢 + 概率跳过 + 事件监听 |
| 13 | 机器人行为引擎 | 发红包行为实现 | 1 | WaitSend阶段识别发送者 + 延迟发送 |
| 14 | 机器人行为引擎 | 游戏结束离场行为实现 | 0.5 | 延迟离场执行 |
| 15 | 机器人标识与余额同步 | Player/Room 扩展 is_robot 标记 | 0.5 | domain 模型 + Redis Lua 脚本适配 |
| 16 | 机器人标识与余额同步 | BillRecord 新增 is_robot 字段 | 0.5 | 仅统计用途，不影响结算逻辑 |
| 17 | 机器人标识与余额同步 | 余额同步与低余额下线 | 1 | 游戏结束同步 + 低于阈值自动停用 |
| 18 | 管理 API | 账号管理 + 监控 API | 1.5 | 创建/查询/启停/监控数据 |
| 19 | 集成测试 | 机器人完整流程联调测试 | 2 | 补位→抢红包→发红包→结算→离场 全链路 |
| 20 | 集成测试 | 边界条件与异常场景测试 | 1 | 余额不足/发红包超时/服务重启/并发 |
| **合计** | | | **18** | |

### 甘特图（依赖关系）
```
Week 1 (Day 1-5):
  ├── 账号管理模块 (Task 1-5)                         [4.5人天]

Week 2 (Day 6-10):
  ├── 机器人行为引擎 (Task 10-14)                      [6人天]
  ├── 机器人标识与余额同步 (Task 15-17)                 [2人天]

Week 3 (Day 11-15):
  ├── 机器人调度系统 (Task 6-9)                         [5人天]
  └── 管理 API (Task 18)                               [1.5人天]

Week 4 (Day 16-18):
  └── 集成测试 (Task 19-20)                            [3人天]
```

### 人天变化说明
与初版方案相比，主要变化：
1. **结算模块大幅简化（-4人天）**: 机器人走正常平台结算流程，无需修改 Settlement Service
2. **去掉真人替换机制（-1.5人天）**: 游戏结束后机器人自动离场，不做中途替换
3. **游戏结束行为简化（-0.5人天）**: 统一为离场，无"留下/离开"决策
4. **管理模块简化（-4人天）**: 不开发后台页面、去掉策略配置API、去掉操作日志API、去掉充值API
5. **去掉配置中心模块（-2.5人天）**: 初期不做策略配置中心，参数走配置文件
6. **调度触发简化（-1人天）**: 仅定时巡检，去掉事件驱动触发
7. **预充值不在系统内（-0.5人天）**: 充值由平台侧操作，初始化脚本无需对接充值API
8. **测试精简（-1.5人天）**: 去掉性能压测、精简边界测试
9. **新增发红包行为（+1人天）**: 机器人需在WaitSend阶段发红包
10. **总计从 30 人天缩减至 18 人天**

> 注：以上为单人全职开发人天估算。若多人并行开发（建议2-3人），按模块分工可压缩至 1.5-2 周完成。
