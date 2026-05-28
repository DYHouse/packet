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

### 3.2 模块二：机器人调度系统 (Robot Scheduler)

#### 功能点
- **定时巡检**: 周期扫描所有 Waiting 房间，对有已准备真人但人数不足开局的房间分配机器人补位
- **分配策略**:
  - 根据房间费用等级匹配机器人账号（虚拟余额检查）
  - 同一房间最多分配 N 个机器人（可配置，避免全是机器人）
  - 至少需要 1 个真人才允许开局（可配置）
- **机器人回收**: 游戏结束后，机器人自动离场回到账号池

#### 调度流程
```
定时巡检 (可配置间隔, 如5秒):
  扫描所有 Waiting 房间
  → 有已准备真人 且 总人数 < 开局所需人数?
    → 是: 从RobotPool获取空闲机器人 → 检查虚拟余额 → 分配到房间
    → 否: 继续扫描下一个房间
```

#### 影响文件
- **新建**: `backend/game/application/robot_scheduler_service.go` — 调度核心逻辑
- **新建**: `backend/game/infrastructure/persistence/redis/robot_scheduler.go` — 调度状态存储
- **修改**: `backend/game/application/game_app_service.go` — GameEnd 事件触发机器人回收

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
- **新建**: `backend/game/domain/robot_behavior.go` — 行为引擎核心逻辑
- **新建**: `backend/game/scheduler/robot_behavior_scheduler.go` — 行为定时调度
- **修改**: `backend/game/application/seat_app_service.go` — 支持机器人选座/准备入口
- **修改**: `backend/game/application/game_app_service.go` — 支持机器人抢红包/发红包入口

---

### 3.4 模块四：机器人身份识别与虚拟结算通道 (Robot Identity & Virtual Settlement Channel)

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

### 总计：约 17.5 人天

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
| 16 | 机器人标识与虚拟结算通道 | Player/Room 扩展 is_robot 标记 | 0.5 | domain 模型 + Redis Lua 脚本适配 |
| 17 | 机器人标识与虚拟结算通道 | BillRecord 新增 is_robot 字段 | 0.5 | 仅统计用途，不影响结算逻辑 |
| 18 | 机器人标识与虚拟结算通道 | RobotChecker 机器人身份识别 | 0.5 | Redis SET + IsRobot 判断 |
| 19 | 机器人标识与虚拟结算通道 | Settlement 虚拟通道（4个修改点） | 1.5 | 扣款/入账/结算/余额虚拟通道 |
| 20 | 机器人标识与虚拟结算通道 | 虚拟余额同步与低余额下线 | 1 | 定时同步 + 低于阈值自动停用 |
| 21 | 集成测试 | 机器人完整流程联调测试 | 2 | 补位→抢红包→发红包→虚拟结算→离场 全链路 |
| 22 | 集成测试 | 边界条件与异常场景测试 | 1 | 虚拟余额不足/发红包超时/Redis宕机/并发 |
| **合计** | | | **17.5** | |

### 与原方案人天变化说明
1. **新增虚拟余额服务（+1.5人天）**: VirtualBalanceService Redis + DB 双层管理
2. **结算虚拟通道替代原结算模块（+0.5人天）**: 4 个修改点的虚拟通道分支
3. **新增 RobotChecker（+0.5人天）**: 机器人身份识别服务
4. **管理 API 移除（-2人天）**: 暂不开发管理 API
5. **集成测试调整（+0.5人天）**: 新增虚拟余额相关边界测试
6. **总计从 18 人天调整至 17.5 人天**
