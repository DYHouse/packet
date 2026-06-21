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
- **调度规则与行为参数全部可配置**，无需改代码即可调整运营策略

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
6. 真人作为旁观者进入房间，等当前局游戏结束后机器人自动离场，真人主动选空座入局
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
│  RobotConfig (配置中心)                                │
│  - 加载 game.yaml robot 段 | 热更新                     │
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
- **配置管理**: 调度规则、行为参数统一由 `game.yaml` 的 `robot` 段管理，通过 Nacos 下发，支持运行时热更新

---

## 3. 模块设计与功能点

### 3.1 模块一：机器人账号管理 (Robot Account Management)

#### 功能点
- **机器人账号批量创建**: 先创建 User 记录（合成平台 UserID），再创建 RobotAccount 记录，建立 1:1 关联
- **虚拟余额初始化**: 创建时设定初始虚拟余额（如 10000 分），存储在 Redis + DB
- **账号状态管理**: 激活/停用/余额不足标记，余额不足时自动下线
- **账号池管理**: 维护可用机器人账号池，按房间等级分配账号（确保机器人虚拟余额匹配房间费用要求）
- **账号信息模拟**: 随机昵称生成（本地化名称库）、随机头像分配，使机器人看起来像不同玩家

#### 与 User 表的关系

机器人首先是 User，其次才是 RobotAccount。两张表 1:1 关联。User 表新增 `is_robot` 标识，DB 层面区分机器人与真人：

```
┌─────────────────────────────────┐     ┌─────────────────────────────────────┐
│          users (基础身份表)       │     │      robot_accounts (机器人扩展表)    │
├─────────────────────────────────┤     ├─────────────────────────────────────┤
│ id          int64  PK (雪花)     │◄────│ user_id           int64  UK → users.id│
│ user_id     string UK (mars平台ID)│    │ status            int                 │
│ nickname    string              │     │ virtual_balance   int64              │
│ avatar      string              │     │ min_room_fee      int                 │
│ ip          string              │     │ max_room_fee      int                 │
│ device_id   string              │     │ ... (机器人专属字段)                  │
│ is_robot    bool   default:false │     │ created_at        timestamp           │
│ created_at  timestamp           │     └─────────────────────────────────────┘
│ updated_at  timestamp           │
└─────────────────────────────────┘
```

> **User 表新增 `is_robot` 字段**：虽然机器人 `user_id` 使用 `robot_` 前缀可从语义上区分，但 `is_robot` 布尔字段提供 DB 层面的明确标识，便于查询过滤和运营统计，且作为 Redis SET 丢失时的兜底识别手段。真人玩家该字段为 `false`，机器人为 `true`。

**双 ID 说明**（User 表有两个标识，但 RobotAccount 只需存一个）：

| 字段 | 类型 | 来源 | 使用方 |
|------|------|------|--------|
| `users.id` (int64) | 游戏服务统一ID(雪花) | 内部主键 | **结算层**(int64) + **游戏层**(`FormatID`转string)：RobotChecker.IsRobot、VirtualBalance、BillRecord、SelectSeat、GrabPacket、SendPacket、广播 |
| `users.user_id` (string) | mars平台用户ID | mars平台同步 | **仅记录用** + 真人平台API调用（Debit/Credit/Settle/GetBalance） |

> **澄清**：`users.id`（int64，雪花算法生成）是游戏平台统一使用的用户 ID。游戏层将其转为 string 传输（`FormatID`，因雪花类型怕丢失精度才使用 string）；结算层直接使用 int64。两者本质是**同一个 ID 的不同表示形式**。`users.user_id`（string）仅是从 mars 平台同步过来的平台用户 ID，用于记录和真人调用资金平台 API。
>
> 现有代码中，游戏层方法（`SeatAppService.SelectSeat`、`GameAppService.GrabPacket` 等）使用 `FormatID(users.id)`（string）；结算层（`RobotChecker`、`DeductService` 等）使用 `users.id`（int64）。`UserIDConvertService.GetPlatformUserID(int64)` 用于结算层调用平台 API 前反查 `users.user_id`。
>
> **RobotAccount 只需存储 `UserID`（int64, → users.id）**：游戏层通过 `FormatID(UserID)` 即可转换得到 string 形式，无需反查 User 表；机器人不调用平台 API，不需要 `users.user_id`。`users.user_id` 已存在于 User 表中，需要时按 `users.id` 查询即可。

**机器人 User 记录创建**：机器人没有真实 mars 平台账号，其 `users.user_id` 为合成值（如 `robot_<seq>`），通过内部调用 `UserService.SaveUser` 创建（绕过 Gateway），昵称/头像从名称库随机生成。创建后设置 `is_robot = true`。Nickname/Avatar 存在 User 表中，RobotAccount 不重复存储。

#### 数据模型
```go
// RobotAccount 机器人账号表（与 users 表 1:1 关联）
type RobotAccount struct {
    ID                 int64     `gorm:"primaryKey"`
    UserID             int64     `gorm:"uniqueIndex"`          // 关联 users.id（雪花内部主键，结算层int64 + 游戏层FormatID转string）
    Status             int       // 0=未激活 1=空闲 2=游戏中 3=停用
    VirtualBalance     int64     // 虚拟余额(分), Redis为主, DB定期同步
    TotalVirtualDebit  int64     // 虚拟扣款累计(分)
    TotalVirtualCredit int64     // 虚拟入账累计(分)
    MinRoomFee         int       // 可参与的最低房间费用
    MaxRoomFee         int       // 可参与的最高房间费用
    TotalGames         int       // 总参与局数
    TotalProfit        int64     // 总盈亏(分)
    LastActiveAt       time.Time // 最后活跃时间
    CreatedAt          time.Time
    UpdatedAt          time.Time
}
```

> Nickname/Avatar 不在 RobotAccount 中，统一从 User 表获取（`UserService.GetUserById` 或 Redis 用户缓存）。机器人加入房间广播时，从 User 缓存读取昵称/头像。

#### 虚拟余额 Redis 数据结构
```
robot:virtual_balance:{userID}  →  int64   // 虚拟余额(分), {userID} 为 users.id (int64)
robot:virtual_balance:dirty     →  SET     // 需要同步到DB的 userID(int64) 集合
robot:user_ids                  →  SET     // 所有机器人 users.id (int64) 集合（供结算层 RobotChecker 使用）
```

> `robot:user_ids` 存储的是 `users.id`（int64 转字符串），供结算层 `RobotChecker.IsRobot(userID int64)` 调用 `SISMEMBER` 判断。游戏层通过 Player.IsRobot 标记识别机器人，不查此 SET。

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

#### 机器人账号创建流程

```
批量创建机器人 (init_robot_accounts.go 或管理API):
  1. 生成合成平台 UserID: "robot_<seq>" (如 robot_00001)
  2. 随机昵称 + 随机头像
  3. 调用 UserService.SaveUser("robot_<seq>", nickname, avatar, "", "")
     → 创建 User 记录, 获得 User.ID (int64, 雪花) 和 User.UserID (string, robot_<seq>)
  4. 设置 User.is_robot = true (UPDATE users SET is_robot = true WHERE id = User.ID)
  5. 创建 RobotAccount 记录:
     - UserID = User.ID (int64)
     - Status = 1 (空闲)
     - VirtualBalance = 档位要求 × initial_balance_multi
     - MinRoomFee / MaxRoomFee 按分配策略设定
  6. SADD robot:user_ids {User.ID}          // 结算层 RobotChecker 识别
  7. SET robot:virtual_balance:{User.ID} {VirtualBalance}  // Redis 虚拟余额
  8. SADD robot:pool:available {User.ID}    // 加入可用账号池
```

#### 影响文件
- **修改**: `backend/game/model/user.go` — User 结构新增 `IsRobot` 字段
- **新建**: `backend/game/model/robot_account.go` — 数据模型
- **新建**: `backend/game/infrastructure/persistence/mysql/robot_account_repo.go` — 数据库操作
- **新建**: `backend/game/infrastructure/persistence/redis/robot_pool.go` — 账号池 Redis 缓存 + 虚拟余额 Redis 操作
- **新建**: `backend/game/infrastructure/persistence/redis/virtual_balance.go` — 虚拟余额 Redis 操作
- **新建**: `backend/game/application/robot_account_service.go` — 账号管理业务逻辑
- **新建**: `backend/game/scheduler/virtual_balance_sync.go` — 虚拟余额定时同步调度器
- **新建**: `backend/scripts/init_robot_accounts.go` — 机器人账号初始化脚本（含 User 创建 + is_robot 标记）

---

### 3.2 模块二：机器人调度器 (Robot Scheduler)

#### 设计思路

调度器负责两件事：1) 发现需要补位的房间并分配机器人；2) 游戏结束后回收机器人。它**不直接操作游戏逻辑**，而是委托给 RobotPlayer 执行具体操作（选座、准备等）。

调度器采用**定时巡检 + 事件驱动**混合模式：定时巡检作为主要触发机制（简单可靠），游戏结束事件作为回收触发（实时性好）。

#### 游戏规则约束

现有游戏规则对调度设计的关键约束（均来自代码实测）：
- **5人满员开局**：所有房间 `MaxPlayers=5`（`backend/game/model/config.go:9`），必须 5 人全部 Ready 才触发 3 秒倒计时开局
- **9 档房间费用**：1/5/10/20/30/50/100/200/500（元）（`backend/scripts/init_rooms.go:14-24`），每档机器人需满足不同的余额要求
- **余额要求公式**：`roomFee/5 + roomFee * 9`（首回合平摊 + 9 轮后续房费），如 1 元房需 9.2 元。此公式为**派生计算**，非硬编码常量，房间费变化时自动适配
- **210 个房间**：各档位房间数量不等（1元房10个、10元房50个等）（`backend/scripts/init_rooms.go:26-36`）
- **房间状态**：`RoomStatusWaiting=1` 为补位目标状态（`backend/game/domain/room.go:3-10`）
- **游戏阶段**：`PhaseGrabbing=4` / `PhaseWaitSend=6` / `PhaseGameEnd=7`（`backend/game/domain/game_state.go:3-13`）

#### 调度规则设计

> **可配置性说明**：以下所有规则的阈值参数均来自配置中心（见第 4 章配置设计）。规则逻辑本身固定，阈值可调。

##### 规则一：补位触发条件

只有满足以下**全部条件**的房间才触发补位：

| 条件 | 规则 | 说明 |
|------|------|------|
| 房间状态 | `RoomStatusWaiting` | 仅等待中的房间需要补位 |
| 已准备真人数 | >= `MinRealPlayers` | 至少有 N 个真人已准备，防止全机器人房间 |
| 已选座总人数 | < `MaxPlayers`（5） | 有空座才需要补位（含已准备 + 已选座未准备） |

**不补位的场景**：
- 纯旁观者房间（没有已准备真人）→ 机器人不参与
- 已满员房间（5 座全占）→ 无需补位，即使有人未准备也只等其准备或超时踢出
- 游戏进行中房间 → 机器人不中途加入（中断替换走现有真人替补机制）

> 说明：`MinRealPlayers` 统计的是**已准备**的真人玩家数，确保机器人补位后能尽快触发开局。但"是否需要补位"看的是**已选座总人数**（空座数），而非已准备人数——因为选座未准备的真人也占着座位，机器人不能选已被占的座位。

##### 规则二：机器人分配数量

```
需要机器人数 = MaxPlayers - 当前已选座总人数（含已准备 + 已选座未准备）
实际分配数 = min(需要机器人数, MaxRobotsPerRoom - 房间已有机器人数, 可用机器人池数量)
```

> **关键修正**：计算依据是**已选座总人数**（占座数），而非已准备人数。真人选座后即使未准备也占着座位，机器人只能填补**空座**。

- `MaxRobotsPerRoom`：单房间最大机器人数，防止全机器人房间
- 约束关系：`MaxRobotsPerRoom = MaxPlayers - MinRealPlayers`（启动时校验，不满足则告警并自动修正）

**默认配置（全局）**：

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| MinRealPlayers | 2 | 至少2个真人已准备才补位，避免1真人对4机器人体验差 |
| MaxRobotsPerRoom | 3 | 最多3个机器人，保证真人占多数 |

> 1个真人 + 4个机器人的体验较差（80%是机器人），因此默认至少 2 个真人才补位，机器人最多 3 个。

##### 规则二补充：选座与准备时序场景

> 真人选座后会启动 30s `TimeoutTypeSeat` 定时器（`seat_app_service.go:84-85`），未在 30s 内准备则被踢出。准备时清除该定时器（`seat_app_service.go:233-234`）。机器人补位需正确处理此时序。

**场景一：真人已选座未准备时触发补位**

```
房间状态: 2 真人已准备 + 1 真人已选座未准备（3 座被占，2 空座）
触发: 已准备真人 2 >= MinRealPlayers(2) ✅, 已选座 3 < 5 ✅
分配: 需要机器人数 = 5 - 3 = 2（按占座数算，不是 5-2=3）
结果: 2 个机器人填空座 → 5 座占满 → 等未准备真人准备或超时踢出
  - 真人准备 → 全员准备 → 开局 ✅
  - 真人超时踢出 → 空出 1 座 → 下轮巡检补 1 个机器人
```

**场景二：机器人选座延迟期间真人抢座（竞态）**

```
巡检快照: 2 真人已准备, 3 空座 → 分配 3 个机器人
机器人 A 延迟 2s 后选座 → 成功（此时 1 真人选了座, 剩 2 空座）
机器人 B 延迟 3s 后选座 → 成功（剩 1 空座）
机器人 C 延迟 5s 后选座 → 无空座 → 选座失败 → 归还账号池
```

处理方式：`RobotPlayer.SelectSeat` 选座前检查空座，无空座则返回错误，调度器捕获后将机器人归还账号池。房间限流锁（30s TTL）防止本轮重复分配，下轮巡检重新评估。

**场景三：机器人自身的选座超时**

机器人调用 `SeatAppService.SelectSeat` 同样会设置 30s `TimeoutTypeSeat` 定时器。但行为引擎在选座后 1-3s 内调用 `PlayerReady`（清除定时器），远小于 30s，不会触发踢出。

##### 规则三：机器人选择策略

从机器人账号池中选择机器人时，按以下规则匹配：

| 维度 | 规则 | 说明 |
|------|------|------|
| 虚拟余额 | `VirtualBalance >= 该档位余额要求` | 机器人余额必须满足参与该档位房间的最低要求 |
| 房间等级匹配 | `MinRoomFee <= 房间RoomFee <= MaxRoomFee` | 机器人的可参与费用范围覆盖该房间 |
| 优先级 | 余额最低的机器人优先分配 | 避免高余额机器人消耗在低费用房间 |
| 排除条件 | 已在游戏中、已停用、余额不足 | 跳过不可用的机器人 |

**各档位机器人余额要求**（由公式 `roomFee/5 + roomFee * 9` 派生，非硬编码）：

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

> 建议机器人初始虚拟余额设为对应档位余额要求的 **1.5~2 倍**，留出多局游戏的空间。初始余额在账号创建时设定（见第 4 章 `initial_balance_multi` 配置）。

##### 规则四：房间优先级

当可用机器人数量不足以满足所有待补位房间时，按以下优先级分配（多级排序）：

| 优先级维度 | 排序规则 | 原因 |
|-----------|---------|------|
| 第一级：已准备真人数 | 降序（4人等1人 > 3人等2人） | 差1人就能开局，补位效果最明显 |
| 第二级：等待时长 | 升序（等得久的优先） | 避免低优先级房间长期饥饿 |

```go
// 房间优先级排序：先按已准备真人数降序，再按等待时长升序
func (s *RobotSchedulerService) sortRoomsByPriority(rooms []RoomCandidate) {
    sort.SliceStable(rooms, func(i, j int) bool {
        if rooms[i].ReadyPlayerCount != rooms[j].ReadyPlayerCount {
            return rooms[i].ReadyPlayerCount > rooms[j].ReadyPlayerCount
        }
        return rooms[i].WaitingSince.Before(rooms[j].WaitingSince)
    })
}
```

##### 规则五：分配防抖

防止机器人在短时间内被反复分配和回收：

| 场景 | 规则 | 说明 |
|------|------|------|
| 分配锁 | `robot:assign:{userID}` 分布式锁，TTL `RobotAssignLockTTL`（默认 10s） | 防止同一机器人被同时分配到多个房间 |
| 房间锁 | `robot:room_assign:{roomID}` 限流锁，TTL `RoomAssignLockTTL`（默认 30s） | 防止同一房间在短时间内重复触发分配 |
| 回收冷却 | 机器人回收后 `RecycleCooldown`（默认 60s）内不重新分配 | 防止机器人刚离开又被分配回同一房间 |
| 余量保护 | 账号池保留 `ReserveCount`（默认 5）个机器人 | 避免账号池被完全耗尽 |

> **ReserveCount 与总池规模关系**：`ReserveCount` 应不超过总机器人池的 30%。若总池 10 个，建议 ReserveCount=3 而非 5，否则可用机器人仅 5 个。启动时校验：若 `ReserveCount > 总池数 * 0.5`，输出告警日志。实际可用机器人 = 总池数 - ReserveCount - 游戏中机器人。

##### 规则六：旁观者与真人补位

当真人作为旁观者等待时：
- 游戏进行中：机器人正常参与，不中途替换
- 游戏结束：机器人自动离场（延迟 3-10s），释放座位
- **真人需主动选座**：当前系统旁观者→座位需调用 `SelectSeat`（`backend/game/application/seat_app_service.go:69`），**无自动晋级队列**。机器人离场后空座不会自动分配给旁观者，真人需自行点击空座入局
- 后续如实现旁观者自动排队补位（`auto-seat-and-queue` 方案），可在此基础上联动：机器人离场后自动触发队列首位真人选座

#### 核心结构

```go
type RobotSchedulerService struct {
    accountSvc    *RobotAccountService      // 机器人账号管理
    robotPlayer   *RobotPlayer              // 执行具体游戏操作
    repo          domain.RoomRepository     // 查询房间状态
    dbRepo        domain.DBRepository       // 查询房间配置
    redis         *cRedis.Client            // 分布式锁 + 状态存储
    config        *RobotConfig              // 调度+行为配置
    ctx           context.Context
    cancel        context.CancelFunc
}
```

#### 调度流程详解

```
巡检主循环 (每 ScanInterval 执行一次):
  1. 获取所有 Waiting 状态的房间列表 (从 Redis 扫描)
  2. 过滤出需要补位的房间:
     - 状态 = RoomStatusWaiting
     - 已准备真人 >= MinRealPlayers
     - 已选座总人数 < MaxPlayers（有空座才补位）
  3. 按优先级排序 (已准备真人多 > 等待时长久)
  4. 对每个房间 (按优先级):
     a. 检查房间分配限流锁 (robot:room_assign:{roomID})
     b. 计算需要的机器人数 = MaxPlayers - 已选座总人数
     c. 对每个需要的机器人:
        i.  从 RobotPool 获取空闲机器人 (按房间费用等级匹配，余额最低优先)
        ii. 获取分配锁 (robot:assign:{robotUserID})
        iii. 委托 RobotPlayer.JoinAndReady(roomID, robotUserID) — 仅入房为旁观者 + 调度延迟选座
        iv. 更新机器人状态、记录映射
     d. 设置房间分配限流锁
  5. 检查账号池余量，低于 ReserveCount 时记录告警日志

并发控制:
  - 单次巡检内房间串行处理（避免机器人池竞争）
  - 单次巡检设时间预算上限 (默认 ScanInterval 的 80%)，超时则本轮终止，剩余房间下轮处理
  - 防止单次巡检耗时超过 ScanInterval 导致巡检堆积
```

#### 机器人回收流程

游戏结束时触发回收（两种触发方式）：

**方式一：事件驱动（实时）** — 监听 Kafka `session_end` 事件或 Game Service 内部的 `endGameWithOptions`（`backend/game/application/game_app_service.go:1040`）调用：
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
- **复用现有 TimeoutScheduler 基础设施**：机器人的延迟行为注册到现有的 Redis ZSET 定时系统（`backend/game/scheduler/timeout_scheduler.go`），而非自建定时器。这样可以统一管理所有定时任务，且服务重启后定时任务不丢失。
- **监听游戏事件而非轮询**：行为引擎通过 Kafka 事件（round_settle、session_end 等）和内部回调感知游戏状态变化，而非轮询房间状态。
- **无状态行为决策**：每次行为触发时，根据当前游戏状态 + 配置参数做出决策，无需维护复杂状态机。

#### 核心结构

```go
type RobotBehaviorEngine struct {
    config       *RobotConfig                // 行为配置
    robotPlayer  *RobotPlayer                // 执行具体游戏操作
    scheduler    *scheduler.TimeoutScheduler // 复用现有定时调度器
    accountSvc   *RobotAccountService        // 查询机器人信息
    grabSvc      *GrabService                // 抢红包服务
    redis        *cRedis.Client
}
```

#### 行为触发机制

行为引擎通过以下方式感知游戏状态变化并触发机器人行为：

| 游戏阶段 | 触发方式 | 机器人行为 | 调用方法 |
|---------|---------|-----------|---------|
| 分配到房间 | 调度器分配后回调 | 延迟选座 → 延迟准备 | `RobotPlayer.SelectSeat` → `RobotPlayer.Ready` |
| 抢红包 (PhaseGrabbing=4) | 监听 Kafka `packet_created` 事件 | 延迟抢红包 | `RobotPlayer.GrabPacket` |
| 等待发送 (PhaseWaitSend=6) | 监听 Kafka `round_settle` 事件，识别最小金额获得者 | 若机器人为发送者，延迟发红包 | `RobotPlayer.SendPacket` |
| 游戏结束 (PhaseGameEnd=7) | 监听 Kafka `session_end` 事件 | 延迟离场 | `RobotPlayer.LeaveRoom` |

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

机器人行为延迟**复用现有 TimeoutScheduler**（`backend/game/scheduler/timeout_scheduler.go`，Redis ZSET 持久化）：

```go
// 注册新的超时类型（现有类型: seat/ready/grab/send/replace，新增 robot）
const TimeoutTypeRobot TimeoutType = "robot"

// 延迟触发机器人行为
func (e *RobotBehaviorEngine) scheduleRobotAction(roomID string, robotUserID string, action string, delay time.Duration) {
    data := fmt.Sprintf("%s:%s:%s", robotUserID, action, uuid.New().String()[:8])
    e.scheduler.SetTimeout(context.Background(), TimeoutTypeRobot, roomID, data, delay)
}

// 注册机器人行为处理器 (在 bootstrap/container.go 中，与现有 5 个 handler 并列)
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
- 与现有 5 个 TimeoutType handler 注册方式一致（`bootstrap/container.go:185-189`）

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

**JoinAndReady**：调度器分配机器人时调用，仅入房为旁观者并调度延迟选座（选座/准备由行为引擎延迟执行，模拟真人节奏）
```go
func (p *RobotPlayer) JoinAndReady(ctx context.Context, roomID string, robotUserID string) error {
    // 1. 加入房间为旁观者
    p.roomAppService.JoinRoom(ctx, &JoinRoomRequest{RoomID: roomID, UserID: robotUserID})

    // 2. 检查是否有空座（竞态保护：巡检快照后可能已有真人选座）
    roomState, _ := p.repo.GetRoomState(ctx, roomID)
    if !hasEmptySeat(roomState) {
        return ErrNoEmptySeat // 调度器捕获后归还账号池
    }

    // 3. 调度延迟选座（由行为引擎经 TimeoutScheduler 延迟触发）
    delay := randomDelay(p.config.Behavior.SeatDelayMin, p.config.Behavior.SeatDelayMax)
    p.behaviorEngine.scheduleRobotAction(roomID, robotUserID, "seat", delay)
    return nil
}
```

> **设计修正**：`JoinAndReady` 不再同步选座+准备，改为仅入房 + 调度延迟选座。选座成功后由行为引擎链式调度延迟准备。这与行为引擎的"延迟选座 → 延迟准备"设计一致，且选座前检查空座可处理竞态。

**SelectSeat**：行为引擎延迟调用，选座成功后链式调度延迟准备
```go
func (p *RobotPlayer) SelectSeat(ctx context.Context, roomID string, robotUserID string) error {
    roomState, _ := p.repo.GetRoomState(ctx, roomID)
    seatNo := pickRandomEmptySeat(roomState)
    if seatNo == 0 { // 无空座
        return ErrNoEmptySeat // 行为引擎捕获后归还账号池
    }
    _, err := p.seatAppService.SelectSeat(ctx, &SelectSeatRequest{
        RoomID: roomID, UserID: robotUserID, SeatNo: seatNo,
    })
    if err != nil {
        return err
    }
    // 选座成功 → 链式调度延迟准备
    delay := randomDelay(p.config.Behavior.ReadyDelayMin, p.config.Behavior.ReadyDelayMax)
    p.behaviorEngine.scheduleRobotAction(roomID, robotUserID, "ready", delay)
    return nil
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

#### 3.5.1 机器人身份识别 (RobotChecker)

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

#### 3.5.2 身份标识（三层）

机器人身份在三个层面标识，各有不同用途：

| 层面 | 标识位置 | 用途 | 查询方式 |
|------|---------|------|---------|
| DB 层 | `users.is_robot` (bool) | DB 查询过滤、运营统计、Redis 兜底重建 | SQL WHERE |
| Redis 层 | `robot:user_ids` SET | 结算层 RobotChecker 热路径快速判断 | SISMEMBER O(1) |
| 内存层 | `Player.IsRobot` (bool) | 游戏内调度器/行为引擎识别 | 内存读取 |

> 三层标识各有用途：Redis 层用于结算热路径 O(1) 查询；内存层用于游戏内调度；DB 层用于持久化查询、运营统计和 Redis SET 丢失时的兜底重建（`SELECT id FROM users WHERE is_robot = true` → 重建 `robot:user_ids` SET）。

在 Room/Player 结构中新增 `IsRobot` 标记，用于调度器识别机器人身份。

#### 3.5.3 BillRecord 机器人标记

新增 `is_robot` 字段，用于运营侧区分机器人和真人账单，做独立的盈亏统计（不影响结算逻辑）。

```go
type BillRecord struct {
    // ... 原有字段 ...
    IsRobot bool `gorm:"default:false;index" json:"is_robot"` // 是否为机器人账单
}
```

#### 3.5.4 结算虚拟通道（4 个修改点）

**修改点 1：扣款虚拟通道** — `DeductService.executeSingleDeduct`（`backend/settlement/service/deduct_service.go:193`）

```
原流程: userIDConvert.GetPlatformUserID → platform.Debit → UpdateBillSuccess
虚拟通道:
  if robotChecker.IsRobot(userID):
    → virtualBalance.Deduct(userID, amount)
    → billMgr.UpdateBillSuccess(billID, 0, virtualBalance.Get(userID))
    → return nil  // 跳过平台API调用
  else: 原流程不变
```

**修改点 2：入账虚拟通道** — `GameSettleService.executeSessionCredit`（`backend/settlement/service/game_settle_service.go:312`）

```
原流程: userIDConvert.GetPlatformUserID → platform.Credit → UpdateBillSuccess
虚拟通道:
  if robotChecker.IsRobot(userID):
    → virtualBalance.Credit(userID, bill.Amount)
    → billMgr.UpdateBillSuccess(billID, 0, virtualBalance.Get(userID))
    → return nil  // 跳过平台API调用
  else: 原流程不变
```

**修改点 3：结算虚拟通道** — `GameSettleService.settlePlayer`（`backend/settlement/service/game_settle_service.go:158`）

```
原流程: userIDConvert.GetPlatformUserID → platform.Settle → UpdateGameSettleStatus
虚拟通道:
  if robotChecker.IsRobot(userID):
    → billMgr.UpdateGameSettleStatusByUser(sessionID, userID, BillGameSettleSettled)
    → return nil  // 跳过平台API调用
  else: 原流程不变
```

**修改点 4：余额查询虚拟通道** — `SettlementService.CheckBalance` / `GetUserBalance`（`backend/settlement/service/settlement_service.go:351,372`）

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
- **修改**: `backend/game/model/user.go` — User 结构新增 `IsRobot` 字段（DB 层标识，见 3.1）
- **修改**: `backend/game/domain/room.go` — Player 结构新增 `IsRobot` 标记
- **修改**: `backend/game/infrastructure/persistence/redis/lua_scripts.go` — 选座/加入房间 Lua 脚本传递 is_robot 标记
- **修改**: `backend/settlement/model/bill.go` — 新增 `IsRobot` 字段
- **修改**: `backend/settlement/service/deduct_service.go` — `executeSingleDeduct` 新增机器人虚拟通道分支 + BillRecord 设置 IsRobot
- **修改**: `backend/settlement/service/game_settle_service.go` — `executeSessionCredit` + `settlePlayer` 新增机器人虚拟通道分支 + BillRecord 设置 IsRobot
- **修改**: `backend/settlement/service/settlement_service.go` — `CheckBalance` / `GetUserBalance` 新增机器人虚拟通道分支 + creditRound 中 BillRecord 设置 IsRobot

---

## 4. 配置设计 (Configuration Design)

> **本章节为本次更新新增**。明确所有调度规则与行为参数的可配置性、配置来源、配置层级。

### 4.1 配置来源与加载

机器人配置统一由 `game.yaml` 的 `robot` 段管理，通过 Nacos 配置中心下发（与现有 `timeout`、`platform` 等段一致，`game.yaml:63-76` 已配置 Nacos）。启动时加载，运行时通过 Nacos 监听支持热更新。

```yaml
# game.yaml 新增 robot 段
robot:
  enabled: true                    # 机器人总开关，false 时调度器不启动
  scheduler:
    scan_interval: 5s              # 巡检间隔
    min_real_players: 2            # 最少已准备真人数（全局默认）
    max_robots_per_room: 3         # 单房间最大机器人数（全局默认）
    robot_assign_lock_ttl: 10s     # 分配锁超时
    room_assign_lock_ttl: 30s      # 房间限流锁超时
    recycle_cooldown: 60s          # 回收冷却时间
    reserve_count: 5               # 账号池保留数量
    reserve_ratio_max: 0.3         # 保留数占总池最大比例（超过则告警）
  behavior:
    seat_delay_min: 2s
    seat_delay_max: 5s
    ready_delay_min: 1s
    ready_delay_max: 3s
    grab_delay_min: 1s
    grab_delay_max: 8s
    grab_skip_prob: 0.05
    send_delay_min: 2s
    send_delay_max: 5s
    leave_after_game_min: 3s
    leave_after_game_max: 10s
  account:
    initial_balance_multi: 1.5     # 初始余额 = 档位要求 × 此倍数
    low_balance_threshold: 0.5     # 余额低于档位要求×此倍数时停用
    sync_interval: 30s             # 虚拟余额同步DB间隔
```

### 4.2 配置结构定义

```go
// RobotConfig 机器人总配置
type RobotConfig struct {
    Enabled   bool            `yaml:"enabled"`
    Scheduler SchedulerConfig `yaml:"scheduler"`
    Behavior  BehaviorConfig  `yaml:"behavior"`
    Account   AccountConfig   `yaml:"account"`
}

type SchedulerConfig struct {
    ScanInterval       time.Duration `yaml:"scan_interval"`
    MinRealPlayers     int           `yaml:"min_real_players"`
    MaxRobotsPerRoom   int           `yaml:"max_robots_per_room"`
    RobotAssignLockTTL time.Duration `yaml:"robot_assign_lock_ttl"`
    RoomAssignLockTTL  time.Duration `yaml:"room_assign_lock_ttl"`
    RecycleCooldown    time.Duration `yaml:"recycle_cooldown"`
    ReserveCount       int           `yaml:"reserve_count"`
    ReserveRatioMax    float64       `yaml:"reserve_ratio_max"`
}

type BehaviorConfig struct {
    SeatDelayMin      time.Duration `yaml:"seat_delay_min"`
    SeatDelayMax      time.Duration `yaml:"seat_delay_max"`
    ReadyDelayMin     time.Duration `yaml:"ready_delay_min"`
    ReadyDelayMax     time.Duration `yaml:"ready_delay_max"`
    GrabDelayMin      time.Duration `yaml:"grab_delay_min"`
    GrabDelayMax      time.Duration `yaml:"grab_delay_max"`
    GrabSkipProb      float64       `yaml:"grab_skip_prob"`
    SendDelayMin      time.Duration `yaml:"send_delay_min"`
    SendDelayMax      time.Duration `yaml:"send_delay_max"`
    LeaveAfterGameMin time.Duration `yaml:"leave_after_game_min"`
    LeaveAfterGameMax time.Duration `yaml:"leave_after_game_max"`
}

type AccountConfig struct {
    InitialBalanceMulti float64       `yaml:"initial_balance_multi"`
    LowBalanceThreshold float64       `yaml:"low_balance_threshold"`
    SyncInterval        time.Duration `yaml:"sync_interval"`
}

// 配置校验规则见 4.3
```

### 4.3 配置校验规则

启动时对配置进行校验，不合法则启动失败并输出明确错误：

| 校验项 | 规则 | 不合法处理 |
|--------|------|-----------|
| `MaxRobotsPerRoom + MinRealPlayers` | `<= MaxPlayers(5)` | 自动修正 MaxRobotsPerRoom 并告警 |
| `ReserveCount / 总池数` | `<= ReserveRatioMax` | 告警（不阻断启动） |
| 所有 DelayMin | `< 对应 DelayMax` | 交换 min/max 并告警 |
| `SendDelayMax` | `< 现有 send 超时(30s)` | 告警，机器人可能超时 |
| `GrabSkipProb` | `0 <= x <= 1` | clamp 到 [0,1] |
| `InitialBalanceMulti` | `>= 1.0` | 设为 1.0 并告警 |

### 4.4 可配置性总结

| 配置项 | 全局默认 | 运行时热更新 |
|--------|---------|-------------|
| 机器人总开关 | - | ✅ |
| ScanInterval | ✅ | ✅ |
| MinRealPlayers | ✅(2) | ✅ |
| MaxRobotsPerRoom | ✅(3) | ✅ |
| 各锁 TTL | ✅ | ✅ |
| RecycleCooldown | ✅ | ✅ |
| ReserveCount | ✅ | ✅ |
| 行为延迟 min/max | ✅ | ✅ |
| GrabSkipProb | ✅ | ✅ |
| 初始余额倍数 | ✅ | ✅ |
| 低余额阈值 | ✅ | ✅ |

> **不可配置项**（游戏规则约束，非机器人系统管辖）：MaxPlayers=5、房间费档位、余额要求公式。这些由游戏核心逻辑定义，机器人系统遵循即可。

---

## 5. 数据流路径

### 5.1 机器人补位完整流程
```
1. 真人进入房间 → 选座（启动30s座位超时）→ 点击准备（清除超时）
2. 定时巡检扫描到该房间有已准备真人且有空座（已选座 < 5）
3. RobotScheduler 按空座数计算需要机器人数 = MaxPlayers - 已选座总人数
4. RobotScheduler 从 RobotPool(Redis) 获取空闲机器人
5. 检查机器人虚拟余额是否满足房间要求 (VirtualBalanceService.GetBalance)
6. 调用 RobotPlayer.JoinAndReady: 入房为旁观者 → 调度延迟选座(2-5s) → 选座成功后链式调度延迟准备(1-3s)
7. 机器人选座 → 房间占座数增加 → 广播给真人
8. 游戏开始后，行为引擎监听游戏事件：
   a. 抢红包阶段(PhaseGrabbing) → 延迟1-8s后自动抢
   b. 结算阶段(PhaseSettling) → 识别最小金额获得者
   c. 等待发送阶段(PhaseWaitSend) → 若机器人为发送者，延迟2-5s后发红包
   d. 结算走虚拟通道（不调用平台API）
9. 游戏结束(PhaseGameEnd) → 延迟3-10s后自动离场，回到账号池
10. 真人作为旁观者等待 → 游戏结束时机器人自动离场 → 真人主动选空座
```

### 5.2 机器人扣款流程（虚拟通道）
```
1. Game Service 触发扣款（首回合平摊 / 后续回合房费）
2. DeductService.executeSingleDeduct 被调用
3. RobotChecker.IsRobot(userID) → true
4. VirtualBalanceService.Deduct(userID, amount) → Redis INCRBY
5. BillRecord 创建时 IsRobot=true, Status 直接设为 Success
6. BillMgr.UpdateBillSuccess(billID, 0, virtualBalanceAfter)
7. 更新 RoundSettlement 状态（与真人一致）
```

### 5.3 机器人入账流程（虚拟通道）
```
1. Game Service 触发会话级入账
2. GameSettleService.executeSessionCredit 被调用
3. RobotChecker.IsRobot(userID) → true
4. VirtualBalanceService.Credit(userID, amount) → Redis INCRBY
5. BillRecord 创建时 IsRobot=true, Status 直接设为 Success
6. BillMgr.UpdateBillSuccess(billID, 0, virtualBalanceAfter)
```

### 5.4 机器人结算流程（虚拟通道）
```
1. GameSettleService.SettleGame 遍历所有玩家
2. 对每个 userID:
   a. RobotChecker.IsRobot(userID)?
      → true: 跳过 platform.Settle()，仅更新 BillGameSettleStatus
      → false: 正常调用 platform.Settle()
3. 对每个有 payout 的玩家:
   a. RobotChecker.IsRobot(userID)?
      → true: 走虚拟入账通道（见 5.3）
      → false: 正常调用 platform.Credit()
```

### 5.5 真人玩家流程（完全不变）
```
1. Debit: platform.Debit()  (不变)
2. Credit: platform.Credit()  (不变)
3. Settle: platform.Settle()  (不变)
4. GetBalance: platform.GetBalance()  (不变)
```

---

## 6. 边界条件与异常处理

### 6.1 机器人虚拟余额不足
- 选座前检查虚拟余额是否满足 `totalRequired`（调用 VirtualBalanceService.GetBalance）
- 发红包前同样需检查虚拟余额（发送者需支付 roomFee）
- 虚拟余额不足时机器人不参与该等级房间，调度器跳过该账号
- 虚拟余额低于 `low_balance_threshold × 档位要求` 时自动停用，通过管理 API 调增虚拟余额后重新激活
- "充值"为纯内部操作，不涉及资金平台

### 6.2 Redis 宕机 / 数据丢失
- 虚拟余额以 Redis 为主存储，DB 为备份
- Redis 重启后从 DB 加载虚拟余额
- 若 Redis 和 DB 同时丢失，可通过 BillRecord 重算（从最后一笔同步点开始）
- 严重情况：标记所有机器人为"待同步"状态，暂停机器人分配直到余额恢复

### 6.3 机器人掉线/服务重启
- Game Service 重启时，从 Redis 恢复机器人状态
- 机器人游戏中的状态持久化到 Redis（标记为 robot player）
- 重启后恢复游戏中的机器人行为调度（复用 TimeoutScheduler 的 Redis ZSET 持久化）
- 虚拟余额从 DB 加载到 Redis

### 6.4 全机器人房间
- 单房间最大机器人数限制（`MaxRobotsPerRoom`），防止全机器人房间
- 至少需要 `MinRealPlayers` 个已准备真人才允许补位（可配置，默认 2）

### 6.5 选座时序与竞态
- 真人选座后 30s 内未准备会被踢出（`TimeoutTypeSeat`），机器人按占座数（非准备数）计算补位数量
- 机器人选座前检查空座，无空座（真人抢先选座）则返回 `ErrNoEmptySeat`，机器人归还账号池
- 房间限流锁（30s TTL）防止同一房间短时间内重复分配，下轮巡检重新评估空座
- 机器人选座同样触发 30s 座位超时，但 1-3s 内准备即清除，不会触发踢出
- 真人超时踢出后空出座位，下轮巡检自动补位

### 6.6 并发安全
- 机器人分配使用 Redis 分布式锁，防止同一机器人被分配到多个房间
- 虚拟余额操作使用 Redis INCRBY（原子操作），多个机器人同时扣款/入账不会产生竞态条件
- Lua 脚本选座复用现有原子操作逻辑

### 6.7 机器人扣款失败
- 虚拟余额不足时扣款失败（INCRBY 结果为负），BillRecord 标记 Failed
- 走现有结算重试机制处理（与真人扣款失败流程一致）
- 调度器在分配前检查虚拟余额，尽量避免此情况

### 6.8 机器人发红包超时
- 机器人必须在 WaitSend 超时（30秒，`game.yaml:10`）前完成发红包
- 行为引擎保证机器人在 2-5 秒内发送，远小于超时时间，正常情况不会超时
- 若行为引擎因故障未能触发，走系统代发逻辑
- 机器人不会触发罚款流程（罚款仅针对真人玩家）

### 6.9 账目一致性
- 机器人 BillRecord 与真人一样完整创建，包含金额、状态、时间等
- 运营侧可通过 `is_robot` 字段分别统计机器人和真人的盈亏
- 虚拟余额 = 初始余额 + 累计入账 - 累计扣款，可随时通过 BillRecord 验算

---

## 7. 预期成果

### 7.1 功能成果
- 完整的机器人补位系统，支持自动补位、行为模拟
- 机器人能完整参与游戏流程：抢红包 + 发红包，与真人行为一致
- **机器人扣款/入账/结算走虚拟通道，零资金平台 API 调用**
- 真人玩家资金流程完全不变，零风险
- 管理 API 支持机器人账号管理、虚拟余额管理和监控
- **调度规则与行为参数全部可配置**，支持运行时热更新

### 7.2 性能指标
- **平台 API 调用减少**: 机器人相关调用从 4-5次/机器人/局 降至 **0次**
- **10 个机器人 × 100 局/天**: 从 4000-5000次/天 降至 **0次/天**
- 补位响应时间取决于巡检间隔，配合巡检周期（如5秒）可实现快速补位
- 机器人操作延迟更低（虚拟余额操作为纯内存操作，无需等待外部 HTTP 调用）
- 单实例支持 500+ 机器人同时在线
- 机器人操作延迟模拟真实度 > 95%（不可通过行为模式识别）

### 7.3 可扩展性
- 行为引擎支持插件式扩展新的行为策略
- 后续可增加事件驱动触发（如真人准备后即时触发）提升响应速度
- 配置中心已支持热更新，后续可扩展后台可视化配置页面
- 机器人数量可水平扩展
- 虚拟账本架构为后续更多内部玩家类型（如NPC、测试账号）提供基础
