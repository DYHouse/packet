# 玩家历史游戏记录页面设计方案

> 文档版本：v1.0
> 创建日期：2026-06-25
> 涉及工程：`packet/backend`（Go 后端）、`gogain/packages/gift-box`（PixiJS 前端）

---

## 一、背景与目标

### 1.1 现状

当前抢红包游戏已具备完整的对局与结算数据沉淀能力：

- 后端通过事件驱动将运行时数据落地到 MySQL（`game_sessions`、`session_players`、`rounds`、`round_grab_records`、`bill_record`、`round_settlement` 等 14 张表）。
- 前端通过 WebSocket 命令完成进房、抢包、结算展示，但结算数据仅在房间内即时展示（`RoomProcessPersonalSettlementLayer`、`RoomProcessWinnerRankingLayer`），**玩家退出房间后无法再回顾历史对局**。

### 1.2 问题

- **无历史查询入口**：前端 4 个场景（Lobby/Home/Hall/Room）均无"历史记录"入口。
- **无历史查询接口**：后端 15 个 WS 命令中无任何按用户维度的历史查询命令；Stats 服务仅提供运营看板聚合，不支持玩家明细。
- **数据已就绪但未利用**：`session_players` 表已记录玩家每局累计统计（`total_send`/`total_grab`），盈亏可由两者差值计算；`round_grab_records` 已记录每回合抢包明细，`bill_record` 已记录每笔资金流水，但均无查询接口暴露。

### 1.3 目标

- 为玩家提供"我的历史记录"页面，支持查看历史对局列表、单局详情、个人统计汇总。
- 复用现有数据沉淀，不改变游戏运行时逻辑。
- 遵循现有前后端架构模式（WS 命令 + 场景化页面），保证一致性。

### 1.4 非目标

- 不做运营/管理端的历史查询（已有 Stats 服务覆盖）。
- 不做实时对局回放（仅展示结算数据，不重建动画）。
- 不做跨用户对比/排行榜（终局排行榜已在房间内展示）。
- 不修改现有结算与事件落地逻辑。

---

## 二、需求分析

### 2.1 用户故事

| 编号 | 角色 | 故事 | 优先级 |
|------|------|------|--------|
| US-1 | 玩家 | 我想查看自己最近参与过的对局列表，了解每次的盈亏 | P0 |
| US-2 | 玩家 | 我想查看某局对局的详情，包括每回合的抢包结果 | P0 |
| US-3 | 玩家 | 我想查看自己的累计统计（总盈亏、总局数、胜率等） | P1 |
| US-4 | 玩家 | 我想按时间范围筛选历史记录 | P2 |
| US-5 | 玩家 | 我想查看某局对局的资金流水明细 | P2 |

### 2.2 功能范围

**P0（首期必做）**：
1. 历史对局列表页（分页加载，按时间倒序）
2. 单局详情页（回合明细 + 个人结果）
3. 个人统计概览（总局数、总盈亏、胜场数）

**P1（次期）**：
4. 时间范围筛选（近 7 天/30 天/自定义）
5. 按房间类型筛选

**P2（远期）**：
6. 单局资金流水明细
7. 导出历史记录

---

## 三、技术方案选型

### 3.1 通信协议选型

| 方案 | 优点 | 缺点 | 决策 |
|------|------|------|------|
| **A. WebSocket 命令** | 与现有前端通信方式一致；复用鉴权与连接管理；无需新增 HTTP 路由 | 大数据量分页需多次请求；WS 断连时无法查询 | **采用** |
| B. HTTP REST API | 适合复杂查询与分页；可独立缓存 | 前端需新增 HTTP 客户端；需重复实现鉴权 | 不采用 |
| C. 混合（WS 命令 + HTTP 详情） | 灵活 | 复杂度高，两套鉴权 | 不采用 |

**决策理由**：前端 `gift-box` 当前仅有一个 WS 端点（`endpoints.ts` 中 `WS_BASE_URL`），所有数据交互均走 WS 命令。为保持架构一致性，历史查询也走 WS 命令，复用现有 `RoomWebSocketClient.request()` 机制。

### 3.2 后端实现位置选型

| 方案 | 优点 | 缺点 | 决策 |
|------|------|------|------|
| **A. 在 Game 服务新增命令** | 与现有命令分发一致；复用 DBRepository | Game 服务职责膨胀 | **采用** |
| B. 在 Stats 服务新增接口 | 已有查询分层参考 | 需新增 WS 入口；Stats 是独立进程，玩家 WS 不连接它 | 不采用 |
| C. 新建独立 history 服务 | 职责清晰 | 过度设计；部署复杂 | 不采用 |

**决策理由**：玩家 WS 连接的是 Gateway → Game 服务链路，历史查询应作为 Game 服务的新命令，在 `generic_service.go` 的 `switch req.Cmd` 中新增分支，复用现有 `DBRepository`。

### 3.3 前端场景选型

新增独立场景 `history`，与 `home`/`hall`/`room` 平级，通过 `NAV_COMMANDS.PUSH` 进入，`POP` 返回。

---

## 四、后端设计

### 4.1 新增 WS 命令

在 `common/message/types.go` 新增命令常量：

```go
// 玩家历史相关命令
const (
    CmdGetPlayerHistory       = "get_player_history"        // 历史对局列表
    CmdGetPlayerSessionDetail = "get_player_session_detail" // 单局详情
    CmdGetPlayerStats         = "get_player_stats"          // 个人统计概览
)
```

在 `config/gateway-router.yaml` 新增路由：

```yaml
routes:
  # ... 现有路由 ...
  - cmd_prefix: "get_player_history"
    service: "game-service"
  - cmd_prefix: "get_player_session_detail"
    service: "game-service"
  - cmd_prefix: "get_player_stats"
    service: "game-service"
```

### 4.2 命令分发

在 `game/server/generic_service.go` 的 `Forward` 方法 `switch req.Cmd` 中新增：

```go
case message.CmdGetPlayerHistory:
    resp = s.handleGetPlayerHistory(ctx, req)
case message.CmdGetPlayerSessionDetail:
    resp = s.handleGetPlayerSessionDetail(ctx, req)
case message.CmdGetPlayerStats:
    resp = s.handleGetPlayerStats(ctx, req)
```

### 4.3 应用服务层

新建 `game/application/history_service.go`：

```go
type HistoryService struct {
    dbRepo   domain.DBRepository
    billMgr  *settlement.BillManager // 复用现有 BillManager
    logger   logger.Logger
}

func NewHistoryService(dbRepo domain.DBRepository, billMgr *settlement.BillManager, logger logger.Logger) *HistoryService {
    return &HistoryService{dbRepo: dbRepo, billMgr: billMgr, logger: logger}
}
```

#### 4.3.1 历史对局列表

```go
// GetPlayerHistory 获取玩家历史对局列表
// 请求参数：page, page_size, start_date?, end_date?
func (s *HistoryService) GetPlayerHistory(ctx context.Context, userID int64, req PlayerHistoryReq) (*PlayerHistoryResp, error)
```

**查询逻辑**：
1. 从 `session_players` 表按 `user_id` 分页查询，按 `joined_at DESC` 排序
2. 关联 `game_sessions` 表获取会话信息（房间号、配置名、回合数、开始/结束时间）
3. 盈亏由 `total_grab - total_send` 计算得出（不取 `total_profit` 字段）
4. 支持时间范围过滤（`joined_at` 字段）

**SQL 示例**：
```sql
SELECT sp.session_id, sp.nickname, sp.avatar, sp.seat_no,
       sp.send_count, sp.grab_count, sp.total_send, sp.total_grab,
       sp.joined_at, sp.left_at,
       gs.room_no, gs.config_name, gs.room_fee, gs.max_rounds, gs.actual_rounds,
       gs.status, gs.started_at, gs.ended_at, gs.end_reason
FROM session_players sp
INNER JOIN game_sessions gs ON sp.session_id = gs.session_id
WHERE sp.user_id = ?
  AND sp.joined_at >= ?
  AND sp.joined_at < ?
ORDER BY sp.joined_at DESC
LIMIT ? OFFSET ?
```

**响应结构**：
```go
type PlayerHistoryResp struct {
    List     []PlayerHistoryItem `json:"list"`
    Total    int64               `json:"total"`
    Page     int                 `json:"page"`
    PageSize int                 `json:"page_size"`
}

type PlayerHistoryItem struct {
    SessionID    string          `json:"session_id"`     // 会话ID
    RoomNo       string          `json:"room_no"`        // 房间号
    ConfigName   string          `json:"config_name"`    // 房间配置名
    RoomFee      currency.Money  `json:"room_fee"`       // 房费(元)
    MaxRounds    int             `json:"max_rounds"`     // 最大回合数
    ActualRounds int             `json:"actual_rounds"`  // 实际回合数
    Status       int             `json:"status"`         // 会话状态 0进行中 1完成 2异常
    StartedAt    int64           `json:"started_at"`     // 开始时间(ms)
    EndedAt      int64           `json:"ended_at"`       // 结束时间(ms)
    EndReason    string          `json:"end_reason"`     // 结束原因
    // 玩家个人数据
    SeatNo       int             `json:"seat_no"`        // 座位号
    SendCount    int             `json:"send_count"`     // 发红包次数
    GrabCount    int             `json:"grab_count"`     // 抢红包次数
    TotalSend    currency.Money  `json:"total_send"`     // 总发出(元)
    TotalGrab    currency.Money  `json:"total_grab"`     // 总抢到(元)
    Profit       currency.Money  `json:"profit"`         // 盈亏(元) = total_grab - total_send
    JoinedAt     int64           `json:"joined_at"`      // 加入时间(ms)
    LeftAt       int64           `json:"left_at"`        // 离开时间(ms)
}
```

> **金额字段说明**：所有金额字段统一使用 `currency.Money` 类型（内部以分存储，JSON 序列化时自动输出为元的数值，如 `1250` 分 → `12.50`）。复用现有 `currency.NewMoneyFromFen()` 构造，与 `grab_packet`、`get_user_balance` 等命令的金额返回方式一致。盈亏不使用 `session_players.total_profit` 字段，而由 `total_grab - total_send` 计算后转为 `currency.Money` 返回。

#### 4.3.2 单局详情

```go
// GetPlayerSessionDetail 获取玩家某局对局详情
// 请求参数：session_id
func (s *HistoryService) GetPlayerSessionDetail(ctx context.Context, userID int64, sessionID int64) (*PlayerSessionDetailResp, error)
```

**查询逻辑**：
1. 校验玩家是否属于该会话（`session_players` 表存在记录）
2. 查询会话基本信息（`game_sessions`）
3. 查询该会话所有回合（`rounds` 表，按 `round_no` 升序）
4. 查询玩家在每回合的抢包记录（`round_grab_records` 表，按 `user_id` 过滤）

**响应结构**：
```go
type PlayerSessionDetailResp struct {
    Session      SessionInfo       `json:"session"`       // 会话信息
    MyStats      PlayerHistoryItem `json:"my_stats"`      // 我的统计
    Rounds       []RoundDetail     `json:"rounds"`        // 回合明细
}

type RoundDetail struct {
    RoundID      string         `json:"round_id"`
    RoundNo      int            `json:"round_no"`
    SenderID     string         `json:"sender_id"`      // 发红包者ID
    SenderType   string         `json:"sender_type"`    // player/system/system_forced
    TotalAmount  currency.Money `json:"total_amount"`   // 红包总额(元)
    StartedAt    int64          `json:"started_at"`
    EndedAt      int64          `json:"ended_at"`
    Status       int            `json:"status"`         // 回合状态
    // 我的抢包结果（可能为空，表示该回合我未抢到或未参与）
    MyGrab       *GrabDetail `json:"my_grab,omitempty"`
}

type GrabDetail struct {
    PacketID      string         `json:"packet_id"`
    Amount        currency.Money `json:"amount"`         // 抢到金额(元)
    IsMin         bool           `json:"is_min"`         // 是否最小(下轮发包)
    IsAutoAssigned bool          `json:"is_auto_assigned"` // 是否系统分配
    GrabbedAt     int64          `json:"grabbed_at"`
}
```

#### 4.3.3 个人统计概览

```go
// GetPlayerStats 获取玩家累计统计
func (s *HistoryService) GetPlayerStats(ctx context.Context, userID int64) (*PlayerStatsResp, error)
```

**查询逻辑**：
1. 跨会话聚合 `session_players` 表：`COUNT(*)`、`SUM(total_send)`、`SUM(total_grab)`、`SUM(send_count)`、`SUM(grab_count)`
2. 盈亏由 `SUM(total_grab) - SUM(total_send)` 计算得出（不使用 `total_profit` 字段）
3. 计算胜场数：`COUNT(*) WHERE (total_grab - total_send) > 0`
4. 可选：查询最近 30 天的每日盈亏趋势（参考 Stats 服务的 `GetDailyTrend` 模式）

**SQL 示例**：
```sql
SELECT
    COUNT(*) AS total_games,
    SUM(total_send) AS total_send,
    SUM(total_grab) AS total_grab,
    SUM(send_count) AS total_send_count,
    SUM(grab_count) AS total_grab_count,
    SUM(CASE WHEN (total_grab - total_send) > 0 THEN 1 ELSE 0 END) AS win_count
FROM session_players
WHERE user_id = ?
```

**响应结构**：
```go
type PlayerStatsResp struct {
    TotalGames     int64          `json:"total_games"`      // 总局数
    WinCount       int64          `json:"win_count"`        // 盈利局数
    LoseCount      int64          `json:"lose_count"`       // 亏损局数
    WinRate        float64        `json:"win_rate"`         // 胜率
    TotalProfit    currency.Money `json:"total_profit"`     // 总盈亏(元) = total_grab - total_send
    TotalSend      currency.Money `json:"total_send"`       // 总发出(元)
    TotalGrab      currency.Money `json:"total_grab"`       // 总抢到(元)
    TotalSendCount int64          `json:"total_send_count"` // 总发包次数
    TotalGrabCount int64          `json:"total_grab_count"` // 总抢包次数
    AvgProfit      currency.Money `json:"avg_profit"`       // 场均盈亏(元)
}
```

### 4.4 仓储层扩展

在 `game/domain/db_repository.go` 新增接口：

```go
type HistoryDBRepository interface {
    // 分页查询玩家历史会话（关联 game_sessions）
    ListPlayerSessions(userID int64, startTime, endTime time.Time, limit, offset int) ([]PlayerSessionRow, int64, error)
    // 查询会话详情（含所有回合）
    GetSessionRounds(sessionID int64) ([]model.Round, error)
    // 查询玩家在某会话的抢包记录
    ListPlayerGrabRecords(sessionID, userID int64) ([]model.RoundGrabRecord, error)
    // 玩家累计统计聚合
    AggregatePlayerStats(userID int64) (*PlayerStatsAggregate, error)
}
```

在 `game/infrastructure/persistence/mysql/` 新建 `history_repository.go` 实现。

### 4.5 数据一致性说明

- 历史查询读取的是已落地的 MySQL 数据，与运行时 Redis 状态分离，不影响对局。
- 盈亏由 `total_grab - total_send` 计算得出，不依赖 `session_players.total_profit` 字段（该字段在会话结束时由 `handleSessionEnd` 事件覆盖写入，进行中会话为 0）。`total_send` 与 `total_grab` 在每回合结算时由 `handleRoundSettle` 事件累加，进行中会话也有累计值。列表查询仍建议过滤 `status = 1`（已完成）的会话，避免展示未结束对局的不完整数据。
- `round_grab_records` 在 `handleRoundSettle` 事件中创建，存在短暂延迟（事件异步消费），玩家刚结束对局后立即查询可能存在秒级延迟，属可接受范围。

### 4.6 限流与安全

- 复用 Gateway 现有 `RateLimit` 中间件（`/ws` 路由已配置）。
- 在 Game 服务 `handleGetPlayerHistory` 中追加用户级限流（参考 `grab_packet` 的 `userLimiter.AllowGrab` 模式）：每用户每秒最多 1 次历史查询。
- 校验 `userID` 必须来自鉴权上下文，不接受客户端传入的 `user_id` 参数（防越权）。

---

## 五、前端设计

### 5.1 新增场景

在 `src/game/createGameScene.ts` 扩展 `GameSceneId`：

```typescript
export type GameSceneId = 'lobby' | 'home' | 'hall' | 'room' | 'history'
```

在 `loadGameScene` 中新增动态 import：

```typescript
case 'history':
  const { HistoryScene } = await import('../scene/HistoryScene')
  return new HistoryScene(ctx)
```

### 5.2 目录结构

遵循现有 widget 目录组织约定：

```
src/
├── scene/
│   └── HistoryScene.ts                    # 历史场景
├── history/
│   ├── HistoryScreen.ts                   # 屏幕主体（列表页）
│   ├── HistoryDetailScreen.ts             # 详情屏幕（单局详情）
│   ├── types.ts                           # 数据类型定义
│   ├── fetchPlayerHistory.ts              # 列表数据拉取
│   ├── fetchPlayerSessionDetail.ts        # 详情数据拉取
│   ├── fetchPlayerStats.ts                # 统计数据拉取
│   └── widgets/
│       ├── history-stats-card/            # 顶部统计卡片
│       │   ├── HistoryStatsCard.ts
│       │   ├── DESIGN.md
│       │   └── index.ts
│       ├── history-list-item/             # 对局列表项
│       │   ├── HistoryListItem.ts
│       │   ├── DESIGN.md
│       │   └── index.ts
│       ├── history-round-item/            # 回合明细项
│       │   ├── HistoryRoundItem.ts
│       │   └── index.ts
│       └── history-bottom-dock/           # 底部返回栏
│           ├── HistoryBottomDock.ts
│           └── index.ts
```

### 5.3 场景定义

`src/scene/HistoryScene.ts`：

```typescript
export class HistoryScene {
  onInit(ctx: SceneContext): void {
    // 1. 等待 WS 连接
    // 2. 拉取统计数据 + 首页列表
    // 3. 创建 HistoryScreen
    // 4. 绑定交互事件
  }
  onResize(vw: number, vh: number): void { /* 同步设计缩放 */ }
  onDestroy(): void { /* 销毁资源 */ }
}
```

### 5.4 数据拉取

参考现有 `fetchRoomTypeChannels.ts` / `fetchHallRoomSlots.ts` 模式：

```typescript
// src/history/fetchPlayerHistory.ts
export const fetchPlayerHistory = async (
  deps: GiftBoxDeps,
  params: { page: number; page_size: number; start_date?: string; end_date?: string }
): Promise<PlayerHistoryResp> => {
  const resp = await deps.roomNetwork.client.request('get_player_history', params)
  if (resp.code !== 0) {
    throw new WsRequestError(resp.cmd, resp.code, resp.msg)
  }
  return resp.data as PlayerHistoryResp
}
```

### 5.5 数据类型

`src/history/types.ts`：

```typescript
export type PlayerHistoryItem = {
  session_id: string
  room_no: string
  config_name: string
  room_fee: number        // 元
  max_rounds: number
  actual_rounds: number
  status: number
  started_at: number
  ended_at: number
  end_reason: string
  seat_no: number
  send_count: number
  grab_count: number
  total_send: number      // 元
  total_grab: number      // 元
  profit: number          // 元 = total_grab - total_send
  joined_at: number
  left_at: number
}

export type PlayerHistoryResp = {
  list: PlayerHistoryItem[]
  total: number
  page: number
  page_size: number
}

export type PlayerStats = {
  total_games: number
  win_count: number
  lose_count: number
  win_rate: number
  total_profit: number    // 元 = total_grab - total_send
  total_send: number      // 元
  total_grab: number      // 元
  total_send_count: number
  total_grab_count: number
  avg_profit: number      // 元
}

export type RoundDetail = {
  round_id: string
  round_no: number
  sender_id: string
  sender_type: string
  total_amount: number    // 元
  started_at: number
  ended_at: number
  status: number
  my_grab?: GrabDetail
}

export type GrabDetail = {
  packet_id: string
  amount: number          // 元
  is_min: boolean
  is_auto_assigned: boolean
  grabbed_at: number
}

export type PlayerSessionDetailResp = {
  session: SessionInfo
  my_stats: PlayerHistoryItem
  rounds: RoundDetail[]
}
```

> **金额展示说明**：后端返回的金额字段均为元（由 `currency.Money` 序列化），前端直接使用 `formatApiMoneyAmount(value)` 格式化展示，与现有 `HomeScreen`、`RoomScreen` 等场景的金币条展示方式一致。盈亏字段 `profit` / `total_profit` 可用 `formatPendingCreditDisplay(value)` 展示带正负号的格式。

### 5.6 页面布局

#### 5.6.1 历史列表页（HistoryScreen）

```
┌─────────────────────────────────┐
│  [返回]  历史记录        [金币条] │  ← 顶栏（复用 CoinWalletBlock）
├─────────────────────────────────┤
│  ┌───────────────────────────┐  │
│  │ 总局数 128  胜率 62%       │  │
│  │ 总盈亏 +12,345            │  │  ← HistoryStatsCard（统计概览）
│  │ 总抢到 98,765  总发出 86,420│  │
│  └───────────────────────────┘  │
├─────────────────────────────────┤
│  ┌───────────────────────────┐  │
│  │ 房间 #A102  10局  2小时前  │  │
│  │ 盈亏 +350                 │  │  ← HistoryListItem（点击进详情）
│  └───────────────────────────┘  │
│  ┌───────────────────────────┐  │
│  │ 房间 #B205  8局   5小时前  │  │
│  │ 盈亏 -120                 │  │
│  └───────────────────────────┘  │
│  ...                            │  ← 可滚动列表
│                                 │
│        [加载更多]                │  ← 分页加载
└─────────────────────────────────┘
```

**交互**：
- 点击列表项 → emit `history.interaction.item_tap` → 拉取详情 → 切换到 `HistoryDetailScreen`
- 滚动到底部 → 自动加载下一页（page + 1）
- 下拉刷新 → 重置 page=1

#### 5.6.2 单局详情页（HistoryDetailScreen）

```
┌─────────────────────────────────┐
│  [返回]  房间 #A102  10局       │
├─────────────────────────────────┤
│  ┌───────────────────────────┐  │
│  │ 我的盈亏 +350             │  │
│  │ 抢到 1,200  发出 850      │  │  ← 个人结果卡片（仅金额，无次数）
│  └───────────────────────────┘  │
├─────────────────────────────────┤
│  回合明细                        │
│  ┌───────────────────────────┐  │
│  │ 第1局  系统发包  抢到 150  │  │
│  │ 红包总额 1,000             │  │  ← HistoryRoundItem
│  └───────────────────────────┘  │
│  ┌───────────────────────────┐  │
│  │ 第2局  我发包   抢到 0     │  │
│  │ 红包总额 800               │  │
│  └───────────────────────────┘  │
│  ...                            │  ← 可滚动
└─────────────────────────────────┘
```

### 5.7 入口位置

在首页 `HomeBottomDock` 新增"历史"图标按钮（复用 `DockIconButton` 组件）：

```typescript
// src/home/widgets/home-bottom-dock/HomeBottomDock.ts
const historyBtn = createDockIconButton(
  i18n.t('home.history'),
  0,  // 无角标
  () => emit(HOME_INTERACTION.historyPress)
)
```

在 `GameController.setupOrchestration` 中监听：

```typescript
eventBus.on(HOME_INTERACTION.historyPress, () => {
  eventBus.emit(NAV_COMMANDS.PUSH, { to: 'history' })
})
```

也可在大厅 `HallBottomDock` 同步新增入口，便于玩家从大厅进入。

### 5.8 事件定义

在 `src/events/homeEventNames.ts` 新增：

```typescript
export const HOME_INTERACTION = {
  // ... 现有 ...
  historyPress: 'home.interaction.history_press',
}
```

在 `src/events/GameEventMap.ts` 新增：

```typescript
export interface GameEventMap {
  // ... 现有 ...
  'history.interaction.item_tap': { sessionId: string }
  'history.interaction.back_click': void
  'history.interaction.load_more': void
  'history.interaction.refresh': void
}
```

### 5.9 i18n 国际化

在 `src/core/systems/i18n/locales/zh.ts`、`en.ts`、`es.ts` 新增 `history.*` 前缀的 key：

```typescript
// zh.ts
history: {
  title: '历史记录',
  stats: {
    total_games: '总局数',
    win_rate: '胜率',
    total_profit: '总盈亏',
    total_grab: '总抢到',
    total_send: '总发出',
    avg_profit: '场均盈亏',
  },
  list: {
    empty: '暂无历史记录',
    load_more: '加载更多',
    loading: '加载中...',
    rounds_label: '{0}局',
    profit_label: '盈亏',
    time_ago: '{0}前',
  },
  detail: {
    title: '房间 {0}',
    my_result: '我的结果',
    my_profit: '我的盈亏',
    grab_amount_label: '抢到 {0}',
    send_amount_label: '发出 {0}',
    rounds: '回合明细',
    round_no: '第{0}局',
    sender_me: '我发包',
    sender_system: '系统发包',
    sender_player: '玩家发包',
    grab_amount: '抢到 {0}',
    no_grab: '未抢到',
    total_amount: '红包总额 {0}',
  },
  back: '返回',
}
```

### 5.10 资源与样式

- **复用现有资源**：`btn-back.png`、`coin-bar.svg`、`icon-coin-01.png`
- **新增资源**（可选）：历史图标（`icon-history.svg`）、空状态插画（`empty-history.svg`）
- **配色**：盈亏正数用金色（`#f0c040`），负数用红色（`#e85050`），中性用深红（`#a64343`），与现有结算层配色一致
- **字体**：复用 Kavoon（标题）+ Nunito Sans（正文）

### 5.11 性能考量

- **分页加载**：每页 20 条，避免一次性加载过多数据
- **列表虚拟化**：若列表超过 100 项，考虑只渲染可视区域（PixiJS 可通过 `visible` 控制）
- **详情懒加载**：点击列表项时才拉取详情数据，列表页不预加载
- **资源按需加载**：`HistoryScene` 通过动态 import 实现，不影响首屏加载
- **图片缓存**：玩家头像复用 `AvatarView` 组件，已内置纹理缓存

---

## 六、数据流与时序

### 6.1 进入历史页面时序

```
玩家点击"历史"按钮
  ↓
HomeBottomDock emit HOME_INTERACTION.historyPress
  ↓
GameController 监听 → emit NAV_COMMANDS.PUSH { to: 'history' }
  ↓
NavigationSystem 压栈 → emit NAV_EVENTS.SCENE_CHANGED
  ↓
wireSceneNavigation 链式执行 → loadGameScene('history')
  ↓
HistoryScene.onInit:
  ├─ waitForWsConnected (确保 WS 已连接)
  ├─ 并行拉取:
  │   ├─ fetchPlayerStats() → get_player_stats 命令
  │   └─ fetchPlayerHistory(page=1) → get_player_history 命令
  ├─ 创建 HistoryStatsCard (渲染统计)
  └─ 创建 HistoryScreen (渲染列表)
```

### 6.2 查看单局详情时序

```
玩家点击列表项
  ↓
HistoryListItem emit history.interaction.item_tap { sessionId }
  ↓
HistoryScreen 监听:
  ├─ 显示 loading
  ├─ fetchPlayerSessionDetail(sessionId) → get_player_session_detail 命令
  └─ 渲染 HistoryDetailScreen (替换列表视图)
```

### 6.3 后端命令处理时序

```
Gateway 收到 WS 命令 get_player_history
  ↓
MessageRouter.Route → forwardToService (gRPC)
  ↓
Game GenericService.Forward:
  ├─ 从 ctx 取 userID (鉴权上下文)
  ├─ switch cmd → handleGetPlayerHistory
  └─ HistoryService.GetPlayerHistory:
      └─ HistoryDBRepository.ListPlayerSessions (MySQL 查询) → 返回响应
```

---

## 七、数据库索引建议

为支持历史查询性能，需在以下表新增索引（若不存在）：

```sql
-- session_players 表：按用户查询历史
ALTER TABLE session_players ADD INDEX idx_user_joined (user_id, joined_at DESC);

-- round_grab_records 表：按会话+用户查询抢包记录
ALTER TABLE round_grab_records ADD INDEX idx_session_user (session_id, user_id, grabbed_at);

-- rounds 表：按会话查询回合列表
ALTER TABLE rounds ADD INDEX idx_session_roundno (session_id, round_no);

-- bill_record 表：按用户+会话查询流水（P2 功能用）
ALTER TABLE bill_record ADD INDEX idx_user_session (user_id, session_id, created_at);
```

**验证方式**：执行 `EXPLAIN` 确认查询走索引，避免全表扫描。

---

## 八、错误处理

### 8.1 后端错误码

在 `common/message/errors.go` 新增：

```go
const (
    CodeHistoryQueryFailed    = 5001 // 历史查询失败
    CodeSessionNotFound       = 5002 // 会话不存在
    CodePlayerNotInSession    = 5003 // 玩家不在该会话中
    CodeHistoryParamInvalid   = 5004 // 参数校验失败
)
```

### 8.2 前端错误处理

- WS 请求失败 → 显示 toast 提示（复用 `ui.cmd.toast` 事件）
- 列表为空 → 显示空状态插画 + 文案"暂无历史记录"
- 详情加载失败 → 显示重试按钮
- WS 断连 → 显示"网络已断开，请重新连接"提示（复用现有断连处理）

---

## 九、测试方案

### 9.1 后端单元测试

- `HistoryService` 各方法的参数校验、边界条件
- 仓储层 SQL 查询正确性（使用 SQLite 内存库或 testcontainers + MySQL）
- 限流逻辑

### 9.2 后端集成测试

- 端到端：完成一局对局后，立即查询历史，验证数据一致性
- 跨用户隔离：用户 A 无法查询用户 B 的历史
- 分页正确性：100 条数据分 5 页查询

### 9.3 前端测试

- 场景切换：Home → History → Home（POP 返回）
- 列表分页加载：滚动加载下一页
- 详情页渲染：回合明细展示
- 空状态、加载中、错误状态
- 横竖屏适配

### 9.4 压测

- 模拟 1000 并发用户同时查询历史，验证 DB 查询性能
- 单用户历史 500 局，验证分页查询响应时间 < 200ms

---

## 十、实施计划

### 10.1 阶段划分

| 阶段 | 内容 | 依赖 |
|------|------|------|
| 阶段 1 | 后端：新增命令、Service、Repository | 无 |
| 阶段 2 | 后端：单元测试 + 集成测试 | 阶段 1 |
| 阶段 3 | 前端：新增场景、数据拉取、列表页 | 阶段 1（接口联调） |
| 阶段 4 | 前端：详情页、统计卡片、i18n | 阶段 3 |
| 阶段 5 | 前端：入口集成、横竖屏适配、测试 | 阶段 4 |
| 阶段 6 | 联调 + 压测 + 上线 | 阶段 2 + 5 |

### 10.2 涉及文件清单

**后端新增**：
- `game/application/history_service.go`
- `game/infrastructure/persistence/mysql/history_repository.go`
- `game/domain/history_repository.go`（接口定义，可选合并到 `db_repository.go`）

**后端修改**：
- `common/message/types.go` — 新增命令常量
- `common/message/errors.go` — 新增错误码
- `config/gateway-router.yaml` — 新增路由
- `game/server/generic_service.go` — 新增命令分发
- `game/bootstrap/container.go` — 注册 HistoryService

**前端新增**：
- `src/scene/HistoryScene.ts`
- `src/history/` 目录下所有文件（见 5.2）

**前端修改**：
- `src/game/createGameScene.ts` — 扩展 `GameSceneId` 与 `loadGameScene`
- `src/events/GameEventMap.ts` — 新增事件
- `src/events/homeEventNames.ts` — 新增 `historyPress`
- `src/core/GameController.ts` — 监听 `historyPress` → PUSH
- `src/home/widgets/home-bottom-dock/HomeBottomDock.ts` — 新增历史按钮
- `src/core/systems/i18n/locales/zh.ts`、`en.ts`、`es.ts` — 新增 `history.*` 翻译
- `src/assets/ensureSceneBundles.ts` — 新增 history 资源包（如有新资源）

**数据库**：
- 新增索引（见第七节）

---

## 十一、风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| 进行中会话数据不完整 | 列表显示盈亏不准 | 列表过滤 `status = 1`（已完成）的会话；盈亏由 `total_grab - total_send` 计算，不依赖会话结束时才写入的 `total_profit` 字段 |
| 事件异步落地延迟 | 刚结束对局立即查询可能缺数据 | 列表页下拉刷新；详情页加载失败时提示"数据同步中，请稍后重试" |
| 历史数据量大导致查询慢 | 列表加载缓慢 | 分页（每页 20 条）+ 索引优化 |
| WS 断连无法查询历史 | 用户体验中断 | 复用现有断连重连机制；断连时显示提示 |
| 玩家头像 URL 失效 | 列表头像加载失败 | `AvatarView` 已内置 fallback 占位图 |
| 跨时区时间显示 | 时间显示不一致 | 统一使用时间戳（ms），前端按设备时区格式化 |

---

## 十二、未来扩展

1. **查询缓存**：一期直查 DB，后续若查询量增大，可参考 Stats 服务的缓存模式（Redis 5min TTL + 空结果防穿透 + SessionEnd 事件触发失效）
2. **对局回放**：基于 `round_grab_records` + `packets` 表重建抢包动画（需扩展数据）
3. **好友历史**：查看好友的历史战绩（需好友系统支持）
4. **成就系统**：基于历史数据解锁成就（如"连胜 10 局"、"累计抢到 10 万"）
5. **数据导出**：支持导出 CSV/Excel 格式的历史记录
6. **每日盈亏趋势图**：参考 Stats 服务的 `GetDailyTrend`，在统计卡片中展示折线图
7. **房间类型筛选**：按 `config_name` 过滤历史记录
8. **盈亏排行榜**：跨会话的玩家盈亏排名（需考虑隐私与反作弊）

---

## 附录 A：现有数据模型参考

### A.1 session_players 表（玩家会话统计）

```go
type SessionPlayer struct {
    ID          int64     `gorm:"primaryKey;autoIncrement"`
    SessionID   int64     `gorm:"uniqueIndex:idx_session_user;not null"`
    RoomID      int64     `gorm:"index;not null"`
    UserID      int64     `gorm:"uniqueIndex:idx_session_user;not null"`
    Nickname    string     `gorm:"size:50"`
    Avatar      string     `gorm:"size:255"`
    SeatNo      int
    SendCount   int        // 发红包次数
    GrabCount   int        // 抢红包次数
    TotalSend   int64      // 累计发出金额（历史查询用此字段）
    TotalGrab   int64      // 累计抢到金额（历史查询用此字段）
    TotalProfit int64      // 累计盈亏（历史查询不使用，改用 TotalGrab-TotalSend 计算）
    IP          string     `gorm:"size:45"`
    DeviceID    string     `gorm:"size:100"`
    JoinedAt    time.Time
    LeftAt      *time.Time
    CreatedAt   time.Time  `gorm:"autoCreateTime"`
}
```

### A.2 round_grab_records 表（抢红包明细）

```go
type RoundGrabRecord struct {
    ID             int64     `gorm:"primaryKey;autoIncrement"`
    RoundID        int64     `gorm:"index;not null"`
    PacketID       int64     `gorm:"index;not null"`
    SessionID      int64     `gorm:"index;not null"`
    UserID         int64     `gorm:"index;not null"`
    Amount         int64     `gorm:"not null"`
    IsMin          int       `gorm:"default:0"`
    IsAutoAssigned int       `gorm:"default:0"`
    GrabbedAt      time.Time
    CreatedAt      time.Time `gorm:"autoCreateTime"`
}
```

### A.3 rounds 表（回合记录）

```go
type Round struct {
    RoundID       int64       `gorm:"primaryKey"`
    SessionID     int64       `gorm:"index;not null"`
    RoomID        int64       `gorm:"index;not null"`
    RoundNo       int         `gorm:"not null;index:idx_session_round"`
    Status        RoundStatus `gorm:"default:0;index"`
    SenderID      int64       `gorm:"default:0"`
    SenderType    string      `gorm:"size:20;default:''"`
    TotalAmount   int64       `gorm:"default:0"`
    StartedAt     *time.Time
    EndedAt       *time.Time
    CreatedAt     time.Time   `gorm:"autoCreateTime"`
    UpdatedAt     time.Time   `gorm:"autoUpdateTime"`
}
```

---

## 附录 B：现有 WS 命令清单（参考）

| 命令 | 用途 | 响应字段 |
|------|------|----------|
| `join_room` | 加入房间 | room_id, room_no, room_state, is_spectator |
| `auto_match` | 自动匹配 | room_id, room_no, room_state, is_spectator |
| `leave_room` | 离开房间 | nil |
| `room_state` | 房间状态 | room_state |
| `select_seat` | 选座 | seat_no, room_state |
| `cancel_seat` | 取消选座 | room_state |
| `player_ready` | 准备 | room_state |
| `send_packet` | 发红包 | packet_count |
| `grab_packet` | 抢红包 | packet_id, amount, position, is_last |
| `get_room_list` | 房间列表 | list[], total, page, page_size |
| `get_room_type_list` | 房间类型列表 | list[] |
| `reconnect` | 重连 | room_id, room_state |
| `get_user_balance` | 用户余额 | balance, pending_credit |
| **`get_player_history`** | **历史对局列表（新增）** | list[], total, page, page_size |
| **`get_player_session_detail`** | **单局详情（新增）** | session, my_stats, rounds[] |
| **`get_player_stats`** | **个人统计（新增）** | total_games, win_count, total_profit, ... |

---

## 附录 C：参考文件路径

### 后端
- 数据模型：`packet/backend/game/model/{session.go, round.go, packet.go, user.go}`
- 领域接口：`packet/backend/game/domain/db_repository.go`
- MySQL 仓储：`packet/backend/game/infrastructure/persistence/mysql/`
- 命令分发：`packet/backend/game/server/generic_service.go`
- 路由配置：`packet/backend/config/gateway-router.yaml`
- 消息类型：`packet/backend/common/message/types.go`
- Stats 参考模式：`packet/backend/stats/{handler, service, repository, dto}/`
- BillManager 查询方法：`packet/backend/settlement/service/bill_manager.go`
- 事件落地逻辑：`packet/backend/game/infrastructure/messaging/game_event_consumer.go`

### 前端
- 场景定义：`gogain/packages/gift-box/src/scene/{HomeScene, HallScene, RoomScene}.ts`
- 场景注册：`gogain/packages/gift-box/src/game/createGameScene.ts`
- 导航系统：`gogain/packages/gift-box/src/core/systems/navigation/index.ts`
- 网络层：`gogain/packages/gift-box/src/net/{endpoints, RoomWebSocketClient, wsTypes}.ts`
- 事件系统：`gogain/packages/gift-box/src/events/{GameEventBus, GameEventMap, homeEventNames}.ts`
- 用户系统：`gogain/packages/gift-box/src/core/systems/user/index.ts`
- 组件模式参考：`gogain/packages/gift-box/src/home/widgets/`、`gogain/packages/gift-box/src/hall/widgets/`
- 结算层参考：`gogain/packages/gift-box/src/scene/room-process-layers/{RoomProcessPersonalSettlementLayer, RoomProcessWinnerRankingLayer}.ts`
- i18n：`gogain/packages/gift-box/src/core/systems/i18n/locales/{zh, en, es}.ts`
- 数据拉取参考：`gogain/packages/gift-box/src/home/fetchRoomTypeChannels.ts`、`gogain/packages/gift-box/src/hall/fetchHallRoomSlots.ts`
