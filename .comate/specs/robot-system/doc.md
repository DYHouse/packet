# 机器人系统方案设计文档 (Robot System Design Document)

## 1. 需求场景与处理逻辑

### 1.1 业务痛点
游戏刚开始推广，玩家上座率不高，导致真人玩家在房间中长时间等待，影响游戏体验和留存率。需要一个机器人系统模拟真人玩家补位，提高房间上座率和游戏启动效率。

### 1.2 核心目标
- 当房间真人不足时，自动分配机器人补位，使游戏能够快速开始
- 机器人行为需模拟真人，包括入座、准备、抢红包、发红包等操作，带有随机延迟
- **机器人使用虚拟账号，所有资金操作（扣款/入账/结算/余额查询）均在游戏系统内部完成，不与资金平台交互，零平台 API 调用**
- 机器人余额由游戏系统自行管理（虚拟账本），与资金平台完全隔离
- 真人玩家的资金操作保持不变

### 1.3 虚拟账号方案

> **核心原则：机器人与资金平台完全隔离**

**问题**：原方案中每个机器人每局产生 4-5 次资金平台 API 调用（Debit + Credit + Settle + GetBalance），10 个机器人 × 100 局/天 = 4000-5000 次/天。

**方案**：机器人不再拥有真实资金平台账号，改用游戏系统内部维护的虚拟账本。所有机器人资金操作走内部通道，BillRecord 直接标记 `Success`，跳过平台 API 调用。

| 维度 | 原方案（真实账号） | 虚拟账号方案 |
|------|-------------------|-------------|
| 机器人平台账号 | 每个机器人一个真实平台账号 | 无平台账号，仅内部虚拟账本 |
| 扣款(Debit) | 调用 platform.Debit() | 内部扣减虚拟余额，BillRecord 直接 Success |
| 入账(Credit) | 调用 platform.Credit() | 内部增加虚拟余额，BillRecord 直接 Success |
| 结算(Settle) | 调用 platform.Settle() | 跳过，仅更新内部统计 |
| 余额查询(GetBalance) | 调用 platform.GetBalance() | 读取 Redis 缓存的虚拟余额 |
| 平台 API 调用量 | 4-5次/机器人/局 | **0次/机器人/局** |
| 机器人充值 | 需平台侧操作 | 内部调增虚拟余额即可 |
| 余额不足下线 | 调平台API确认余额 | 虚拟余额 < 阈值即下线 |

**已有先例**：系统中 `PlatformAccountID=0`（平台账号）已使用相同模式——`DeductForSystemPacket` 直接创建 `Status=Success` 的 BillRecord，不调用平台 API。机器人虚拟账号是这一模式的扩展。

### 1.4 处理逻辑
1. 真人玩家进入房间、选座、点击准备
2. 定时巡检扫描 Waiting 房间，对有已准备真人但人数不足开局的房间分配机器人补位
3. 机器人通过 Game Service 内部接口执行：进入房间 → 选座 → 准备 → 抢红包/发红包等操作
4. 游戏过程中机器人行为模拟真人节奏（带随机延迟），包括抢红包和发红包
5. 游戏结束后机器人自动离场，回到账号池等待下次分配
6. 真人作为旁观者进入房间，等当前局游戏结束后机器人自动离场，真人选空座入局
7. **机器人所有扣款/入账/结算走内部虚拟通道，不调用资金平台 API**
8. **机器人余额检查/查询走内部虚拟余额，不调用资金平台 API**

---

## 2. 系统架构与技术方案

### 2.1 整体架构

```
┌──────────────────────────────────────────────────────┐
│                  管理 API (HTTP)                      │
│  机器人账号管理 | 监控数据 | 虚拟余额管理（暂不开发）    │
└────────────────────┬─────────────────────────────────┘
                     │ HTTP API
┌────────────────────▼─────────────────────────────────┐
│                   Gateway Service                     │
│  RobotAdminHandler (管理接口)                          │
│  - 机器人账号CRUD | 监控数据（暂不开发）                │
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
│  VirtualBalanceService (虚拟余额服务)                   │
│  - Redis 原子操作 | 余额扣减/增加/查询 | DB同步         │
└────────────────────┬─────────────────────────────────┘
                     │ Kafka
┌────────────────────▼─────────────────────────────────┐
│                Settlement Service                     │
│  RobotChecker (机器人身份识别)                         │
│  - Redis SET, O(1) 查询                               │
│  真人玩家: 正常走 platform.Debit/Credit/Settle         │
│  机器人玩家: 跳过平台API，走虚拟通道内部记账             │
│    - 扣款: VirtualBalance.Deduct + BillRecord Success  │
│    - 入账: VirtualBalance.Credit + BillRecord Success  │
│    - 结算: 跳过 platform.Settle，仅更新内部状态         │
│    - 余额: VirtualBalance.GetBalance (Redis)           │
└──────────────────────────────────────────────────────┘
```

### 2.2 技术选型
- **机器人连接方式**: 在 Game Service 内部实现 `RobotPlayer`，不经过 Gateway WS，直接调用 Game Service 内部方法（选座、准备、抢红包、发红包等），大幅降低复杂度
- **行为模拟**: 基于定时器 + 随机延迟的决策引擎，模拟真人操作节奏
- **调度策略**: 定时巡检驱动，周期扫描 Waiting 房间，按需分配机器人
- **虚拟账本**: 机器人余额由 Redis 管理（INCRBY 原子操作），DB 定期同步，不依赖资金平台
- **结算处理**: 真人走正常平台 API 流程；机器人走虚拟通道，BillRecord 直接标记 Success，零平台 API 调用
- **账号管理**: 机器人账号预创建并存储在数据库，初始虚拟余额由管理 API 设定，无需资金平台充值
- **离场策略**: 游戏结束后机器人统一自动离场，回到账号池等待下次调度分配，不做中途替换

---

## 3. 模块设计与功能点

### 3.1 模块一：机器人账号管理 (Robot Account Management)

#### 功能点
- **机器人账号批量创建**: 支持批量创建机器人账号，生成内部用户记录
- **虚拟余额初始化**: 创建时设定初始虚拟余额（如 10000 分），存储在 Redis + DB
- **账号状态管理**: 激活/停用/余额不足标记，余额不足时自动下线
- **账号池管理**: 维护可用机器人账号池，按房间等级分配账号（确保机器人虚拟余额匹配房间费用要求）
- **账号信息模拟**: 随机昵称生成（本地化名称库）、随机头像分配，使机器人看起来像不同玩家

#### 数据模型
```go
// RobotAccount 机器人账号表
type RobotAccount struct {
    ID                int64     `gorm:"primaryKey"`
    UserID            int64     `gorm:"uniqueIndex"`         // 关联 users.id
    Nickname          string    `gorm:"size:100"`
    Avatar            string    `gorm:"size:512"`
    Status            int       // 0=未激活 1=空闲 2=游戏中 3=停用
    VirtualBalance    int64     // 虚拟余额(分), Redis为主, DB定期同步
    TotalVirtualDebit int64     // 虚拟扣款累计(分)
    TotalVirtualCredit int64    // 虚拟入账累计(分)
    MinRoomFee        int       // 可参与的最低房间费用
    MaxRoomFee        int       // 可参与的最高房间费用
    TotalGames        int       // 总参与局数
    TotalProfit       int64     // 总盈亏(分)
    LastActiveAt      time.Time // 最后活跃时间
    CreatedAt         time.Time
    UpdatedAt         time.Time
}
```

#### 虚拟余额 Redis 数据结构
```
robot:virtual_balance:{userID}  →  int64   // 虚拟余额(分)
robot:virtual_balance:dirty     →  SET     // 需要同步到DB的userID集合
robot:user_ids                  →  SET     // 所有机器人 UserID 集合（供 RobotChecker 使用）
```

#### 虚拟余额操作接口
```go
type VirtualBalanceService struct {
    redis *cRedis.Client
    db    RobotAccountRepository
}

// Deduct 虚拟扣款（原子操作）
func (s *VirtualBalanceService) Deduct(ctx context.Context, userID int64, amount int64) error {
    // INCRBY robot:virtual_balance:{userID} -amount
    // SADD robot:virtual_balance:dirty {userID}
    // 检查扣减后余额是否为负（防御性检查）
}

// Credit 虚拟入账（原子操作）
func (s *VirtualBalanceService) Credit(ctx context.Context, userID int64, amount int64) error {
    // INCRBY robot:virtual_balance:{userID} +amount
    // SADD robot:virtual_balance:dirty {userID}
}

// GetBalance 查询虚拟余额
func (s *VirtualBalanceService) GetBalance(ctx context.Context, userID int64) (int64, error) {
    // GET robot:virtual_balance:{userID}
    // 缓存未命中则从DB加载并设置缓存
}

// SyncToDB 批量同步脏数据到DB（由定时任务调用）
func (s *VirtualBalanceService) SyncToDB(ctx context.Context) error {
    // SMEMBERS robot:virtual_balance:dirty
    // GET robot:virtual_balance:{userID}
    // UPDATE robot_accounts SET virtual_balance = ? WHERE user_id = ?
    // DEL robot:virtual_balance:dirty
}
```

#### 影响文件
- **新建**: `backend/game/model/robot_account.go` — 数据模型
- **新建**: `backend/game/infrastructure/persistence/mysql/robot_account_repo.go` — 数据库操作
- **新建**: `backend/game/infrastructure/persistence/redis/robot_pool.go` — 账号池 Redis 缓存 + 虚拟余额 Redis 操作
- **新建**: `backend/game/infrastructure/persistence/redis/virtual_balance.go` — 虚拟余额 Redis 操作
- **新建**: `backend/game/application/robot_account_service.go` — 账号管理业务逻辑
- **新建**: `backend/game/scheduler/virtual_balance_sync.go` — 虚拟余额定时同步调度器
- **新建**: `backend/scripts/init_robot_accounts.go` — 机器人账号初始化脚本

---

### 3.2 模块二：机器人调度器 (Robot Scheduler)

#### 设计思路

调度器负责两件事：1) 发现需要补位的房间并分配机器人；2) 游戏结束后回收机器人。它**不直接操作游戏逻辑**，而是委托给 RobotPlayer 执行具体操作（选座、准备等）。

调度器采用**定时巡检 + 事件驱动**混合模式：定时巡检作为主要触发机制（简单可靠），游戏结束事件作为回收触发（实时性好）。

#### 游戏规则约束

现有游戏规则对调度设计的关键约束：
- **5人满员开局**：所有房间 MaxPlayers=5，必须 5 人全部 Ready 才触发 3 秒倒计时开局
- **9 档房间费用**：1/5/10/20/30/50/100/200/500（元），每档机器人需满足不同的余额要求
- **余额要求公式**：`roomFee/5 + roomFee * 9`（首回合平摊 + 9 轮后续房费），如 1 元房需 9.2 元
- **210 个房间**：各档位房间数量不等（1元房10个、10元房50个等）

#### 调度规则设计

##### 规则一：补位触发条件

只有满足以下**全部条件**的房间才触发补位：

| 条件 | 规则 | 说明 |
|------|------|------|
| 房间状态 | `RoomStatusWaiting` | 仅等待中的房间需要补位 |
| 真人玩家数 | >= `MinRealPlayers`（默认 1） | 至少有1个真人玩家，防止全机器人房间 |
| 已准备人数 | < `MaxPlayers`（5） | 人数不足才需要补位 |
| 至少1人已准备 | 已准备真人 >= 1 | 避免给无人准备的房间分配机器人 |

**不补位的场景**：
- 纯旁观者房间（没有已准备真人）→ 机器人不参与
- 已满员房间 → 无需补位
- 游戏进行中房间 → 机器人不中途加入（中断替换走现有真人替补机制）

##### 规则二：机器人分配数量

```
需要机器人数 = MaxPlayers - 当前已准备人数
实际分配数 = min(需要机器人数, MaxRobotsPerRoom - 房间已有机器人数, 可用机器人池数量)
```

- `MaxRobotsPerRoom`（默认 4）：单房间最大机器人数，防止全机器人房间
- 与 `MinRealPlayers`（默认 1）联动：`MaxRobotsPerRoom = MaxPlayers - MinRealPlayers`
- 当 `MinRealPlayers=1` 时：1个真人最多配4个机器人

**建议配置**：

| 配置项 | 建议值 | 说明 |
|--------|--------|------|
| MinRealPlayers | 2 | 至少2个真人，避免1真人对4机器人体验差 |
| MaxRobotsPerRoom | 3 | 最多3个机器人，保证真人占多数 |

> 1个真人 + 4个机器人的体验较差（80%是机器人），建议至少 2 个真人才补位，机器人最多 3 个。

##### 规则三：机器人选择策略

从机器人账号池中选择机器人时，按以下规则匹配：

| 维度 | 规则 | 说明 |
|------|------|------|
| 虚拟余额 | `VirtualBalance >= 该档位余额要求` | 机器人余额必须满足参与该档位房间的最低要求 |
| 房间等级匹配 | `MinRoomFee <= 房间RoomFee <= MaxRoomFee` | 机器人的可参与费用范围覆盖该房间 |
| 优先级 | 余额最低的机器人优先分配 | 避免高余额机器人消耗在低费用房间 |
| 排除条件 | 已在游戏中、已停用、余额不足 | 跳过不可用的机器人 |

**各档位机器人余额要求**：

| 房间档位 | RoomFee(分) | 最低虚拟余额(分) | 最低虚拟余额(元) |
|---------|------------|-----------------|-----------------|
| 1元房 | 100 | 920 | 9.2 |
| 5元房 | 500 | 4,600 | 46 |
| 10元房 | 1,000 | 9,200 | 92 |
| 20元房 | 2,000 | 18,400 | 184 |
| 30元房 | 3,000 | 27,600 | 276 |
| 50元房 | 5,000 | 46,000 | 460 |
| 100元房 | 10,000 | 92,000 | 920 |
| 200元房 | 20,000 | 184,000 | 1,840 |
| 500元房 | 50,000 | 460,000 | 4,600 |

> 建议机器人初始虚拟余额设为对应档位余额要求的 **1.5~2 倍**，留出多局游戏的空间。

##### 规则四：房间优先级

当可用机器人数量不足以满足所有待补位房间时，按以下优先级分配：

| 优先级 | 条件 | 原因 |
|--------|------|------|
| 高 | 已准备真人多（4人等1人） | 差1人就能开局，补位效果最明显 |
| 中 | 已准备真人中等（3人等2人） | 常规补位 |
| 低 | 已准备真人少（1~2人） | 补位需求不紧急，优先满足高优先级房间 |

```go
// 房间优先级排序：已准备真人多的房间优先获得机器人
func (s *RobotSchedulerService) sortRoomsByPriority(rooms []RoomCandidate) {
    sort.Slice(rooms, func(i, j int) bool {
        return rooms[i].ReadyPlayerCount > rooms[j].ReadyPlayerCount
    })
}
```

##### 规则五：分配防抖

防止机器人在短时间内被反复分配和回收：

| 场景 | 规则 | 说明 |
|------|------|------|
| 分配锁 | `robot:assign:{userID}` 分布式锁，TTL 10s | 防止同一机器人被同时分配到多个房间 |
| 房间锁 | `robot:room_assign:{roomID}` 限流锁，TTL 30s | 防止同一房间在短时间内重复触发分配 |
| 回收冷却 | 机器人回收后 60s 内不重新分配 | 防止机器人刚离开又被分配回同一房间 |
| 余量保护 | 账号池保留 `ReserveCount`（默认 5）个机器人 | 避免账号池被完全耗尽 |

##### 规则六：旁观者等待真人补位

当真人作为旁观者等待时：
- 游戏进行中：机器人正常参与，不中途替换
- 游戏结束：机器人自动离场（延迟 3-10s），真人可以选空座入局
- 无需特殊处理，现有机制自然支持

#### 核心结构

```go
type RobotSchedulerService struct {
    accountSvc    *RobotAccountService      // 机器人账号管理
    robotPlayer   *RobotPlayer              // 执行具体游戏操作
    repo          domain.RoomRepository     // 查询房间状态
    dbRepo        domain.DBRepository       // 查询房间配置
    redis         *cRedis.Client            // 分布式锁 + 状态存储
    config        RobotSchedulerConfig
    ctx           context.Context
    cancel        context.CancelFunc
}

type RobotSchedulerConfig struct {
    ScanInterval       time.Duration // 巡检间隔，默认 5 秒
    MinRealPlayers     int           // 最少真人玩家数，默认 2
    MaxRobotsPerRoom   int           // 单房间最大机器人数，默认 3
    RobotAssignLockTTL int           // 分配锁超时(秒)，默认 10
    RoomAssignLockTTL  int           // 房间分配限流锁(秒)，默认 30
    RecycleCooldown    time.Duration // 回收冷却时间，默认 60s
    ReserveCount       int           // 账号池保留数量，默认 5
}
```

#### 调度流程详解

```
巡检主循环 (每 ScanInterval 执行一次):
  1. 获取所有 Waiting 状态的房间列表 (从 Redis 扫描)
  2. 过滤出需要补位的房间:
     - 状态 = RoomStatusWaiting
     - 已准备真人 >= MinRealPlayers
     - 已准备总人数 < MaxPlayers
  3. 按优先级排序 (已准备真人多的房间优先)
  4. 对每个房间 (按优先级):
     a. 检查房间分配限流锁 (robot:room_assign:{roomID})
     b. 计算需要的机器人数
     c. 对每个需要的机器人:
        i.  从 RobotPool 获取空闲机器人 (按房间费用等级匹配，余额最低优先)
        ii. 获取分配锁 (robot:assign:{robotUserID})
        iii. 委托 RobotPlayer.JoinAndReady(roomID, robotUserID)
        iv. 更新机器人状态、记录映射
     d. 设置房间分配限流锁
  5. 检查账号池余量，低于 ReserveCount 时记录告警日志
```

#### 机器人回收流程

游戏结束时触发回收（两种触发方式）：

**方式一：事件驱动（实时）** — 监听 Kafka `session_end` 事件或 Game Service 内部的 `endGameWithOptions` 调用：
```
OnGameEnd(roomID):
  1. 查询房间内的机器人列表 (从 robot:room:{roomID} 获取)
  2. 对每个机器人:
     a. 取消机器人在该房间的所有行为调度任务
     b. 延迟 3-10 秒后执行离开房间
     c. 更新 RobotAccount 状态为"空闲"
     d. 归还到 RobotPool
     e. 删除机器人-房间映射
  3. 同步机器人虚拟余额到 DB
```

**方式二：定时巡检兜底** — 在巡检循环中检测已结束房间的残留机器人，防止事件丢失导致机器人无法回收。

#### Redis 数据结构

```
robot:room:{roomID}            →  SET    // 房间内的机器人 UserID 集合
robot:assign:{robotUserID}     →  STRING // 分配锁(值=roomID, TTL防重复分配)
robot:room_assign:{roomID}     →  STRING // 房间分配限流锁(TTL防短时间重复分配)
robot:recycle_cooldown:{userID} → STRING // 回收冷却标记(TTL=冷却时间)
robot:scheduler:active         →  SET    // 所有正在游戏中的机器人 UserID
```

#### 影响文件
- **新建**: `backend/game/application/robot_scheduler_service.go` — 调度核心逻辑（巡检+分配+回收）
- **新建**: `backend/game/infrastructure/persistence/redis/robot_scheduler.go` — 调度状态 Redis 存储
- **修改**: `backend/game/application/game_app_service.go` — `endGameWithOptions` 中触发机器人回收通知

---

### 3.3 模块三：机器人行为引擎 (Robot Behavior Engine)

#### 设计思路

行为引擎是机器人的"大脑"，负责在游戏各阶段做出模拟真人的行为决策。它**不自己实现游戏逻辑**，而是监听游戏状态变化，在合适的时机调用现有的 GameAppService / SeatAppService 的内部方法。

关键设计决策：
- **复用现有 TimeoutScheduler 基础设施**：机器人的延迟行为注册到现有的 Redis ZSET 定时系统，而非自建定时器。这样可以统一管理所有定时任务，且服务重启后定时任务不丢失。
- **监听游戏事件而非轮询**：行为引擎通过 Kafka 事件（round_settle、session_end 等）和内部回调感知游戏状态变化，而非轮询房间状态。
- **无状态行为决策**：每次行为触发时，根据当前游戏状态 + 配置参数做出决策，无需维护复杂状态机。

#### 核心结构

```go
type RobotBehaviorEngine struct {
    config       RobotBehaviorConfig
    robotPlayer  *RobotPlayer              // 执行具体游戏操作
    scheduler    *scheduler.TimeoutScheduler // 复用现有定时调度器
    accountSvc   *RobotAccountService       // 查询机器人信息
    grabSvc      *GrabService               // 抢红包服务
    redis        *cRedis.Client
}

type RobotBehaviorConfig struct {
    // 选座行为
    SeatDelayMin      time.Duration // 选座延迟下限，默认 2s
    SeatDelayMax      time.Duration // 选座延迟上限，默认 5s
    // 准备行为
    ReadyDelayMin     time.Duration // 准备延迟下限，默认 1s
    ReadyDelayMax     time.Duration // 准备延迟上限，默认 3s
    // 抢红包行为
    GrabDelayMin      time.Duration // 抢红包延迟下限，默认 1s
    GrabDelayMax      time.Duration // 抢红包延迟上限，默认 8s
    GrabSkipProb      float64       // 不抢概率，默认 0.05 (5%)
    // 发红包行为
    SendDelayMin      time.Duration // 发红包延迟下限，默认 2s
    SendDelayMax      time.Duration // 发红包延迟上限，默认 5s
    // 离场行为
    LeaveAfterGameMin time.Duration // 游戏后离场延迟下限，默认 3s
    LeaveAfterGameMax time.Duration // 游戏后离场延迟上限，默认 10s
}
```

#### 行为触发机制

行为引擎通过以下方式感知游戏状态变化并触发机器人行为：

| 游戏阶段 | 触发方式 | 机器人行为 | 调用方法 |
|---------|---------|-----------|---------|
| 分配到房间 | 调度器分配后回调 | 延迟选座 → 延迟准备 | `RobotPlayer.SelectSeat` → `RobotPlayer.Ready` |
| 抢红包 (Grabbing) | 监听 Kafka `packet_created` 事件 | 延迟抢红包 | `RobotPlayer.GrabPacket` |
| 等待发送 (WaitSend) | 监听 Kafka `round_settle` 事件，识别最小金额获得者 | 若机器人为发送者，延迟发红包 | `RobotPlayer.SendPacket` |
| 游戏结束 (GameEnd) | 监听 Kafka `session_end` 事件 | 延迟离场 | `RobotPlayer.LeaveRoom` |

#### 各行为详细设计

**选座+准备行为（分配触发）**：
```
调度器分配机器人到房间后:
  1. 随机延迟 SeatDelayMin~SeatDelayMax
  2. 选择一个空座位 (查询 RoomStateData.SeatOwners 找空座)
  3. 调用 SeatAppService.SelectSeat(roomID, robotUserID, seatNo)
  4. 随机延迟 ReadyDelayMin~ReadyDelayMax
  5. 调用 SeatAppService.PlayerReady(roomID, robotUserID)
```

**抢红包行为（事件触发）**：
```
收到 packet_created 事件:
  1. 检查该房间内是否有机器人 (查询 robot:room:{roomID})
  2. 对每个机器人:
     a. 以 GrabSkipProb 概率决定是否跳过 (5%概率不抢)
     b. 若决定抢: 随机延迟 GrabDelayMin~GrabDelayMax
     c. 调用 RobotPlayer.GrabPacket(roomID, roundID, robotUserID)
        内部调用 GameAppService.GrabPacket(roomID, robotUserID, packetID)
     d. packetID 从事件中获取，或从 Redis 查询可用红包
```

**发红包行为（事件触发）**：
```
收到 round_settle 事件:
  1. 从事件中解析最小金额获得者 (MinPlayerID)
  2. 检查 MinPlayerID 是否为机器人
  3. 若是机器人:
     a. 随机延迟 SendDelayMin~SendDelayMax
     b. 调用 RobotPlayer.SendPacket(roomID, robotUserID)
        内部调用 GameAppService.SendPacket(roomID, robotUserID)
     c. 延迟必须 < 现有 SendTimeout (30秒)，确保在超时前完成
```

**离场行为（事件触发）**：
```
收到 session_end 事件 或 GameEnd 回调:
  1. 查询该房间内所有机器人
  2. 对每个机器人:
     a. 随机延迟 LeaveAfterGameMin~LeaveAfterGameMax
     b. 调用 RobotPlayer.LeaveRoom(roomID, robotUserID)
        内部调用 SeatAppService.CancelSeat + RoomAppService.LeaveRoom
     c. 更新 RobotAccount 状态为"空闲"
     d. 归还到 RobotPool
```

#### 延迟实现方式

机器人行为延迟**复用现有 TimeoutScheduler**：

```go
// 注册新的超时类型
const TimeoutTypeRobot TimeoutType = "robot"

// 延迟触发机器人行为
func (e *RobotBehaviorEngine) scheduleRobotAction(roomID string, robotUserID string, action string, delay time.Duration) {
    data := fmt.Sprintf("%s:%s:%s", robotUserID, action, uuid.New().String()[:8])
    e.scheduler.SetTimeout(context.Background(), TimeoutTypeRobot, roomID, data, delay)
}

// 注册机器人行为处理器 (在 bootstrap 中)
scheduler.RegisterHandler(scheduler.TimeoutTypeRobot, behaviorEngine.HandleRobotTimeout)

func (e *RobotBehaviorEngine) HandleRobotTimeout(ctx context.Context, roomID string, data string) {
    // 解析 data: "robotUserID:action:uuid"
    parts := strings.SplitN(data, ":", 3)
    robotUserID, action := parts[0], parts[1]
    switch action {
    case "seat":   e.robotPlayer.SelectSeat(ctx, roomID, robotUserID)
    case "ready":  e.robotPlayer.Ready(ctx, roomID, robotUserID)
    case "grab":   e.robotPlayer.GrabPacket(ctx, roomID, robotUserID)
    case "send":   e.robotPlayer.SendPacket(ctx, roomID, robotUserID)
    case "leave":  e.robotPlayer.LeaveRoom(ctx, roomID, robotUserID)
    }
}
```

**优点**：
- 复用现有定时基础设施，无需新建调度器
- Redis ZSET 持久化，服务重启后机器人行为不丢失
- 统一的定时管理，便于监控和调试

#### 防检测设计

- 所有延迟使用 `rand(min, max)` 生成，每次行为独立随机
- 抢红包不抢概率避免"每局必抢"的规律性
- 不同机器人的延迟独立计算，不会同时行动
- 选座时随机选择空座位，避免固定位置偏好

#### 影响文件
- **新建**: `backend/game/domain/robot_behavior.go` — 行为引擎核心逻辑（决策+延迟计算+防检测）
- **新建**: `backend/game/scheduler/robot_behavior_scheduler.go` — 与现有 TimeoutScheduler 集成
- **修改**: `backend/game/scheduler/timeout_scheduler.go` — 新增 `TimeoutTypeRobot` 类型
- **修改**: `backend/game/bootstrap/container.go` — 注册机器人行为处理器
- **修改**: `backend/game/application/seat_app_service.go` — 支持内部调用入口（不经过 WebSocket）
- **修改**: `backend/game/application/game_app_service.go` — 支持内部调用入口（不经过 WebSocket）
- **修改**: `backend/game/infrastructure/messaging/game_event_consumer.go` — 消费事件时触发行为引擎

---

### 3.4 模块四：机器人玩家 (RobotPlayer)

> RobotPlayer 是机器人的"手脚"，封装了对现有游戏服务的内部调用。调度器和行为引擎通过 RobotPlayer 执行所有游戏操作，而非直接调用 SeatAppService/GameAppService。

#### 设计思路

RobotPlayer 的核心职责是**将真人通过 WebSocket 发起的操作，转换为对 Game Service 内部方法的直接调用**。

现有真人玩家的操作路径：
```
用户操作 → Gateway WS → Gateway Handler → Game Service (SeatAppService/GameAppService)
```

机器人玩家的操作路径：
```
行为引擎 → RobotPlayer → Game Service (SeatAppService/GameAppService)
```

区别：机器人绕过 WebSocket 层，直接调用 Service 方法。这意味着：
- 无需 Gateway 相关的认证/鉴权
- 无需 WebSocket 连接管理
- 调用方式与单元测试中的内部调用一致
- 广播通知仍然正常触发（真人能看到机器人的操作）

#### 核心结构

```go
type RobotPlayer struct {
    seatAppService  *SeatAppService
    gameAppService  *GameAppService
    roomAppService  *RoomAppService
    accountSvc      *RobotAccountService
    grabSvc         *GrabService
    repo            domain.RoomRepository
}

// JoinAndReady 一站式入房+选座+准备（供调度器调用）
func (p *RobotPlayer) JoinAndReady(ctx context.Context, roomID string, robotUserID string) error

// SelectSeat 选座（供行为引擎延迟调用）
func (p *RobotPlayer) SelectSeat(ctx context.Context, roomID string, robotUserID string) error

// Ready 准备（供行为引擎延迟调用）
func (p *RobotPlayer) Ready(ctx context.Context, roomID string, robotUserID string) error

// GrabPacket 抢红包（供行为引擎延迟调用）
func (p *RobotPlayer) GrabPacket(ctx context.Context, roomID string, roundID string, robotUserID string) error

// SendPacket 发红包（供行为引擎延迟调用）
func (p *RobotPlayer) SendPacket(ctx context.Context, roomID string, robotUserID string) error

// LeaveRoom 离场（供行为引擎/调度器调用）
func (p *RobotPlayer) LeaveRoom(ctx context.Context, roomID string, robotUserID string) error
```

#### 各方法实现详解

**JoinAndReady**：调度器分配机器人时调用，执行完整的入场流程
```go
func (p *RobotPlayer) JoinAndReady(ctx context.Context, roomID string, robotUserID string) error {
    // 1. 加入房间为旁观者
    p.roomAppService.JoinRoom(ctx, &JoinRoomRequest{RoomID: roomID, UserID: robotUserID})

    // 2. 随机选择空座位
    roomState, _ := p.repo.GetRoomState(ctx, roomID)
    seatNo := pickRandomEmptySeat(roomState)

    // 3. 选座
    _, err := p.seatAppService.SelectSeat(ctx, &SelectSeatRequest{
        RoomID: roomID, UserID: robotUserID, SeatNo: seatNo,
    })

    // 4. 准备
    _, err = p.seatAppService.PlayerReady(ctx, &SetReadyRequest{
        RoomID: roomID, UserID: robotUserID,
    })
    return err
}
```

**SelectSeat**：行为引擎延迟调用
```go
func (p *RobotPlayer) SelectSeat(ctx context.Context, roomID string, robotUserID string) error {
    roomState, _ := p.repo.GetRoomState(ctx, roomID)
    seatNo := pickRandomEmptySeat(roomState)
    _, err := p.seatAppService.SelectSeat(ctx, &SelectSeatRequest{
        RoomID: roomID, UserID: robotUserID, SeatNo: seatNo,
    })
    return err
}
```

**Ready**：行为引擎延迟调用
```go
func (p *RobotPlayer) Ready(ctx context.Context, roomID string, robotUserID string) error {
    _, err := p.seatAppService.PlayerReady(ctx, &SetReadyRequest{
        RoomID: roomID, UserID: robotUserID,
    })
    return err
}
```

**GrabPacket**：行为引擎延迟调用，需要查询可用红包
```go
func (p *RobotPlayer) GrabPacket(ctx context.Context, roomID string, roundID string, robotUserID string) error {
    // 从 Redis 获取一个可用的红包 ID
    packetID, err := p.grabSvc.GetAvailablePacketID(ctx, roomID, roundID)
    if err != nil {
        return err  // 可能已被抢完，忽略
    }
    _, err = p.gameAppService.GrabPacket(ctx, roomID, robotUserID, packetID)
    return err
}
```

**SendPacket**：行为引擎延迟调用
```go
func (p *RobotPlayer) SendPacket(ctx context.Context, roomID string, robotUserID string) error {
    _, err := p.gameAppService.SendPacket(ctx, roomID, robotUserID)
    return err
}
```

**LeaveRoom**：行为引擎/调度器调用
```go
func (p *RobotPlayer) LeaveRoom(ctx context.Context, roomID string, robotUserID string) error {
    // 1. 取消座位
    p.seatAppService.CancelSeat(ctx, &CancelSeatRequest{
        RoomID: roomID, UserID: robotUserID,
    })
    // 2. 离开房间
    p.roomAppService.LeaveRoom(ctx, &LeaveRoomRequest{
        RoomID: roomID, UserID: robotUserID,
    })
    return nil
}
```

#### 与现有代码的兼容性

RobotPlayer 直接调用 SeatAppService / GameAppService 的现有方法，**不需要修改这些方法的签名或逻辑**。现有方法已经具有完整的参数校验和原子操作（Lua 脚本），机器人调用与真人调用走完全相同的路径，只是跳过了 WebSocket 网关层。

唯一需要调整的地方：现有 `GameAppService.GrabPacket` 方法可能需要增加一个从内部获取 packetID 的入口（而非从 WebSocket 请求参数传入），或通过 GrabService 提供查询可用红包的方法。

#### 影响文件
- **新建**: `backend/game/application/robot_player.go` — RobotPlayer 核心实现
- **修改**: `backend/game/application/grab_service.go` — 新增 `GetAvailablePacketID` 方法（查询可用红包供机器人使用）

---

### 3.5 模块五：机器人身份识别与虚拟结算通道 (Robot Identity & Virtual Settlement Channel)

> **本模块是虚拟账号方案的核心**。解决两个问题：1) 在游戏内标识机器人身份，用于调度和结算分支；2) Settlement Service 中所有平台 API 调用路径为机器人提供"虚拟通道"——跳过平台 API 调用，直接内部记账。

#### 3.4.1 机器人身份识别 (RobotChecker)

在 Settlement Service 中判断某 UserID 是否为机器人，基于 Redis SET 实现，O(1) 查询复杂度。

```go
// Settlement Service 内部接口
type RobotChecker interface {
    IsRobot(ctx context.Context, userID int64) bool
}

type redisRobotChecker struct {
    redis *cRedis.Client
}

func (c *redisRobotChecker) IsRobot(ctx context.Context, userID int64) bool {
    // SISMEMBER robot:user_ids {userID}
    exists, _ := c.redis.SIsMember(ctx, "robot:user_ids", fmt.Sprintf("%d", userID)).Result()
    return exists
}
```

机器人账号创建/加载到 Redis 账号池时，同时 `SADD robot:user_ids {userID}`。

#### 3.4.2 Player 身份标识

在 Room/Player 结构中新增 `IsRobot` 标记，用于调度器识别机器人身份。

#### 3.4.3 BillRecord 机器人标记

新增 `is_robot` 字段，用于运营侧区分机器人和真人账单，做独立的盈亏统计（不影响结算逻辑）。

```go
type BillRecord struct {
    // ... 原有字段 ...
    IsRobot bool `gorm:"default:false;index" json:"is_robot"` // 是否为机器人账单
}
```

#### 3.4.4 结算虚拟通道（4 个修改点）

**修改点 1：扣款虚拟通道** — `DeductService.executeSingleDeduct`

```
原流程: userIDConvert.GetPlatformUserID → platform.Debit → UpdateBillSuccess
虚拟通道:
  if robotChecker.IsRobot(userID):
    → virtualBalance.Deduct(userID, amount)
    → billMgr.UpdateBillSuccess(billID, 0, virtualBalance.Get(userID))
    → return nil  // 跳过平台API调用
  else: 原流程不变
```

**修改点 2：入账虚拟通道** — `GameSettleService.executeSessionCredit`

```
原流程: userIDConvert.GetPlatformUserID → platform.Credit → UpdateBillSuccess
虚拟通道:
  if robotChecker.IsRobot(userID):
    → virtualBalance.Credit(userID, bill.Amount)
    → billMgr.UpdateBillSuccess(billID, 0, virtualBalance.Get(userID))
    → return nil  // 跳过平台API调用
  else: 原流程不变
```

**修改点 3：结算虚拟通道** — `GameSettleService.settlePlayer`

```
原流程: userIDConvert.GetPlatformUserID → platform.Settle → UpdateGameSettleStatus
虚拟通道:
  if robotChecker.IsRobot(userID):
    → billMgr.UpdateGameSettleStatusByUser(sessionID, userID, BillGameSettleSettled)
    → return nil  // 跳过平台API调用
  else: 原流程不变
```

**修改点 4：余额查询虚拟通道** — `SettlementService.CheckBalance` / `GetUserBalance`

```
原流程: userIDConvert.GetPlatformUserID → platform.GetBalance
虚拟通道:
  if robotChecker.IsRobot(userID):
    → virtualBalance.GetBalance(userID)
    → return balance, balance >= requiredAmount
  else: 原流程不变
```

#### 影响文件
- **新建**: `backend/settlement/service/robot_checker.go` — RobotChecker 接口与 Redis 实现
- **修改**: `backend/game/domain/room.go` — Player 结构新增 `IsRobot` 标记
- **修改**: `backend/game/infrastructure/persistence/redis/lua_scripts.go` — 选座/加入房间 Lua 脚本传递 is_robot 标记
- **修改**: `backend/settlement/model/bill.go` — 新增 `IsRobot` 字段
- **修改**: `backend/settlement/service/deduct_service.go` — `executeSingleDeduct` 新增机器人虚拟通道分支 + BillRecord 设置 IsRobot
- **修改**: `backend/settlement/service/game_settle_service.go` — `executeSessionCredit` + `settlePlayer` 新增机器人虚拟通道分支 + BillRecord 设置 IsRobot
- **修改**: `backend/settlement/service/settlement_service.go` — `CheckBalance` / `GetUserBalance` 新增机器人虚拟通道分支 + creditRound 中 BillRecord 设置 IsRobot

---

## 4. 数据流路径

### 4.1 机器人补位完整流程
```
1. 真人进入房间 → 选座 → 点击准备 (Gateway WS → Game Service)
2. 定时巡检扫描到该房间有已准备真人但人数不足开局
3. RobotScheduler 从 RobotPool(Redis) 获取空闲机器人
4. 检查机器人虚拟余额是否满足房间要求 (VirtualBalanceService.GetBalance)
5. 调用 Game Service 内部方法执行: 入房 → 选座(延迟2-5s) → 准备(延迟1-3s)
6. 机器人选座 → 房间玩家数增加 → 广播给真人
7. 游戏开始后，行为引擎监听游戏事件：
   a. 抢红包阶段(Grabbing) → 延迟1-8s后自动抢
   b. 结算阶段(Settling) → 识别最小金额获得者
   c. 等待发送阶段(WaitSend) → 若机器人为发送者，延迟2-5s后发红包
   d. 结算走虚拟通道（不调用平台API）
8. 游戏结束(PhaseGameEnd) → 延迟3-10s后自动离场，回到账号池
9. 真人作为旁观者等待 → 游戏结束时机器人自动离场 → 真人选空座
```

### 4.2 机器人扣款流程（虚拟通道）
```
1. Game Service 触发扣款（首回合平摊 / 后续回合房费）
2. DeductService.executeSingleDeduct 被调用
3. RobotChecker.IsRobot(userID) → true
4. VirtualBalanceService.Deduct(userID, amount) → Redis INCRBY
5. BillRecord 创建时 IsRobot=true, Status 直接设为 Success
6. BillMgr.UpdateBillSuccess(billID, 0, virtualBalanceAfter)
7. 更新 RoundSettlement 状态（与真人一致）
```

### 4.3 机器人入账流程（虚拟通道）
```
1. Game Service 触发会话级入账
2. GameSettleService.executeSessionCredit 被调用
3. RobotChecker.IsRobot(userID) → true
4. VirtualBalanceService.Credit(userID, amount) → Redis INCRBY
5. BillRecord 创建时 IsRobot=true, Status 直接设为 Success
6. BillMgr.UpdateBillSuccess(billID, 0, virtualBalanceAfter)
```

### 4.4 机器人结算流程（虚拟通道）
```
1. GameSettleService.SettleGame 遍历所有玩家
2. 对每个 userID:
   a. RobotChecker.IsRobot(userID)?
      → true: 跳过 platform.Settle()，仅更新 BillGameSettleStatus
      → false: 正常调用 platform.Settle()
3. 对每个有 payout 的玩家:
   a. RobotChecker.IsRobot(userID)?
      → true: 走虚拟入账通道（见 4.3）
      → false: 正常调用 platform.Credit()
```

### 4.5 真人玩家流程（完全不变）
```
1. Debit: platform.Debit()  (不变)
2. Credit: platform.Credit()  (不变)
3. Settle: platform.Settle()  (不变)
4. GetBalance: platform.GetBalance()  (不变)
```

---

## 5. 边界条件与异常处理

### 5.1 机器人虚拟余额不足
- 选座前检查虚拟余额是否满足 `totalRequired`（调用 VirtualBalanceService.GetBalance）
- 发红包前同样需检查虚拟余额（发送者需支付 roomFee）
- 虚拟余额不足时机器人不参与该等级房间，调度器跳过该账号
- 虚拟余额低于最低房间费用时自动停用，通过管理 API 调增虚拟余额后重新激活
- "充值"为纯内部操作，不涉及资金平台

### 5.2 Redis 宕机 / 数据丢失
- 虚拟余额以 Redis 为主存储，DB 为备份
- Redis 重启后从 DB 加载虚拟余额
- 若 Redis 和 DB 同时丢失，可通过 BillRecord 重算（从最后一笔同步点开始）
- 严重情况：标记所有机器人为"待同步"状态，暂停机器人分配直到余额恢复

### 5.3 机器人掉线/服务重启
- Game Service 重启时，从 Redis 恢复机器人状态
- 机器人游戏中的状态持久化到 Redis（标记为 robot player）
- 重启后恢复游戏中的机器人行为调度
- 虚拟余额从 DB 加载到 Redis

### 5.4 全机器人房间
- 单房间最大机器人数限制，防止全机器人房间
- 至少需要 1 个真人才允许开局（可配置）

### 5.5 并发安全
- 机器人分配使用 Redis 分布式锁，防止同一机器人被分配到多个房间
- 虚拟余额操作使用 Redis INCRBY（原子操作），多个机器人同时扣款/入账不会产生竞态条件
- Lua 脚本选座复用现有原子操作逻辑

### 5.6 机器人扣款失败
- 虚拟余额不足时扣款失败（INCRBY 结果为负），BillRecord 标记 Failed
- 走现有结算重试机制处理（与真人扣款失败流程一致）
- 调度器在分配前检查虚拟余额，尽量避免此情况

### 5.7 机器人发红包超时
- 机器人必须在 WaitSend 超时（30秒）前完成发红包
- 行为引擎保证机器人在 2-5 秒内发送，远小于超时时间，正常情况不会超时
- 若行为引擎因故障未能触发，走系统代发逻辑
- 机器人不会触发罚款流程（罚款仅针对真人玩家）

### 5.8 账目一致性
- 机器人 BillRecord 与真人一样完整创建，包含金额、状态、时间等
- 运营侧可通过 `is_robot` 字段分别统计机器人和真人的盈亏
- 虚拟余额 = 初始余额 + 累计入账 - 累计扣款，可随时通过 BillRecord 验算

---

## 6. 预期成果

### 6.1 功能成果
- 完整的机器人补位系统，支持自动补位、行为模拟
- 机器人能完整参与游戏流程：抢红包 + 发红包，与真人行为一致
- **机器人扣款/入账/结算走虚拟通道，零资金平台 API 调用**
- 真人玩家资金流程完全不变，零风险
- 管理 API 支持机器人账号管理、虚拟余额管理和监控

### 6.2 性能指标
- **平台 API 调用减少**: 机器人相关调用从 4-5次/机器人/局 降至 **0次**
- **10 个机器人 × 100 局/天**: 从 4000-5000次/天 降至 **0次/天**
- 补位响应时间取决于巡检间隔，配合巡检周期（如5秒）可实现快速补位
- 机器人操作延迟更低（虚拟余额操作为纯内存操作，无需等待外部 HTTP 调用）
- 单实例支持 500+ 机器人同时在线
- 机器人操作延迟模拟真实度 > 95%（不可通过行为模式识别）

### 6.3 可扩展性
- 行为引擎支持插件式扩展新的行为策略
- 后续可增加事件驱动触发（如真人准备后即时触发）提升响应速度
- 后续可增加策略配置中心和后台页面
- 机器人数量可水平扩展
- 虚拟账本架构为后续更多内部玩家类型（如NPC、测试账号）提供基础

---

## 7. 人天计划表

### 总计：约 21 人天

| 序号 | 模块 | 任务 | 人天 | 说明 |
|------|------|------|------|------|
| 1 | 机器人账号管理 | 数据模型设计与建表 | 0.5 | robot_accounts 表（含虚拟余额字段） |
| 2 | 机器人账号管理 | 账号 CRUD Repository 实现 | 1 | MySQL 读写 + 查询条件 |
| 3 | 机器人账号管理 | 账号池 Redis 管理实现 | 1 | 空闲池/游戏中池/按等级分桶 + robot:user_ids SET |
| 4 | 机器人账号管理 | 虚拟余额服务实现 | 1.5 | Redis INCRBY/GET + DB 同步 + VirtualBalanceService |
| 5 | 机器人账号管理 | 账号管理 Service 实现 | 1.5 | 创建/激活/停用/虚拟余额检查/昵称生成 |
| 6 | 机器人账号管理 | 批量创建脚本 | 0.5 | 初始化脚本，账号创建+加载到Redis+初始虚拟余额 |
| 7 | 机器人调度系统 | 调度器核心框架（定时巡检） | 1.5 | 周期扫描 Waiting 房间 + 评估分配 |
| 8 | 机器人调度系统 | 分配策略（余额检查/数量控制） | 1.5 | 等级匹配/虚拟余额检查/最大机器人数限制 |
| 9 | 机器人调度系统 | 游戏结束后机器人回收 | 1 | 离场 + 虚拟余额同步 + 状态重置 + 回收至账号池 |
| 10 | 机器人调度系统 | 调度状态 Redis 持久化与恢复 | 1 | 机器人-房间映射/服务重启恢复 |
| 11 | 机器人行为引擎 | 行为引擎核心框架 | 2 | 延迟调度器 + 随机决策 + 事件驱动 |
| 12 | 机器人行为引擎 | 入座/准备行为实现 | 1 | 随机选座 + 延迟准备 |
| 13 | 机器人行为引擎 | 抢红包行为实现 | 1.5 | 延迟抢 + 概率跳过 + 事件监听 |
| 14 | 机器人行为引擎 | 发红包行为实现 | 1 | WaitSend阶段识别发送者 + 延迟发送 |
| 15 | 机器人行为引擎 | 游戏结束离场行为实现 | 0.5 | 延迟离场执行 |
| 16 | 机器人行为引擎 | 与现有 TimeoutScheduler 集成 | 1 | 新增 TimeoutTypeRobot + Handler 注册 |
| 17 | 机器人玩家 | RobotPlayer 核心实现 | 2 | JoinAndReady/SelectSeat/Ready/GrabPacket/SendPacket/LeaveRoom |
| 18 | 机器人玩家 | GrabService 新增可用红包查询 | 0.5 | GetAvailablePacketID 供机器人抢红包使用 |
| 19 | 机器人标识与虚拟结算通道 | Player/Room 扩展 is_robot 标记 | 0.5 | domain 模型 + Redis Lua 脚本适配 |
| 20 | 机器人标识与虚拟结算通道 | BillRecord 新增 is_robot 字段 | 0.5 | 仅统计用途，不影响结算逻辑 |
| 21 | 机器人标识与虚拟结算通道 | RobotChecker 机器人身份识别 | 0.5 | Redis SET + IsRobot 判断 |
| 22 | 机器人标识与虚拟结算通道 | Settlement 虚拟通道（4个修改点） | 1.5 | 扣款/入账/结算/余额虚拟通道 |
| 23 | 机器人标识与虚拟结算通道 | 虚拟余额同步与低余额下线 | 1 | 定时同步 + 低于阈值自动停用 |
| 24 | 集成测试 | 机器人完整流程联调测试 | 2 | 补位→抢红包→发红包→虚拟结算→离场 全链路 |
| 25 | 集成测试 | 边界条件与异常场景测试 | 1 | 虚拟余额不足/发红包超时/Redis宕机/并发 |
| **合计** | | | **21** | |

### 与原方案人天变化说明
1. **新增虚拟余额服务（+1.5人天）**: VirtualBalanceService Redis + DB 双层管理
2. **结算虚拟通道替代原结算模块（+0.5人天）**: 4 个修改点的虚拟通道分支
3. **新增 RobotChecker（+0.5人天）**: 机器人身份识别服务
4. **RobotPlayer 独立模块（+2.5人天）**: 封装内部调用 + GrabService 扩展
5. **行为引擎与 TimeoutScheduler 集成（+1人天）**: 复用现有定时基础设施
6. **管理 API 移除（-2人天）**: 暂不开发管理 API
7. **集成测试调整（+0.5人天）**: 新增虚拟余额相关边界测试
8. **总计从 18 人天调整至 21 人天**
