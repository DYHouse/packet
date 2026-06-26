# 历史记录时间显示修复与详情页时间补充方案

> 创建日期：2026-06-26
> 关联 spec：`fix-history-bill-reconciliation`（上一轮对账数据修正引入的副作用）
> 涉及工程：`gogain/packages/gift-box`（PixiJS 前端）
> **本方案仅做分析与设计，暂不改代码**

---

## 一、问题背景

上一轮 `fix-history-bill-reconciliation` spec 将历史列表数据源从 `session_players` 切换到 `bill_record` 聚合后，引入了一个副作用：**列表项的相对时间显示为横线 `—`**。

同时用户提出新需求：**详情页应显示会话的开始时间和结束时间**，便于玩家回顾对局时段。

---

## 二、问题一：列表时间显示为横线

### 2.1 根因定位

**前端**：[HistoryListItem.ts:159-160](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/widgets/history-list-item/HistoryListItem.ts#L159-L160)

```typescript
const timeAgo = new Text({
  text: i18n.t('history.list.time_ago', formatTimeAgo(item.joined_at ?? 0)),
  ...
})
```

`formatTimeAgo` 函数（第 38-49 行）在 `ts <= 0` 时返回 `'—'`：

```typescript
export function formatTimeAgo(ts: number, now: number = Date.now()): string {
  if (!Number.isFinite(ts) || ts <= 0) return '—'
  ...
}
```

**后端**：[history_service.go:220-238](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_service.go#L220-L238) 的 `playerSessionBillRowToItem` 没有设置 `JoinedAt` 字段：

```go
func playerSessionBillRowToItem(row *domain.PlayerSessionBillRow) PlayerHistoryItem {
    return PlayerHistoryItem{
        ...
        StartedAt:    timeToMs(row.StartedAt),  // ✅ 有值（来自 game_sessions.started_at）
        EndedAt:      timeToMs(row.EndedAt),    // ✅ 有值（来自 game_sessions.ended_at）
        // JoinedAt 缺失 → 零值 0 → 前端 formatTimeAgo(0) 返回 '—'
        // LeftAt 缺失 → 零值 0
    }
}
```

`PlayerSessionBillRow` DTO（[db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/db_repository.go)）不含 `JoinedAt`/`LeftAt`/`SeatNo`/`Nickname`/`Avatar` 字段，因为这些字段来自 `session_players` 表，而 bill 聚合查询只 JOIN 了 `game_sessions` + `bill_record`，没有 JOIN `session_players`。

### 2.2 方案对比

| 方案 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| **A. 前端改用 `started_at`** | `HistoryListItem.ts` 将 `item.joined_at` 改为 `item.started_at` | 最小改动；`started_at` 语义更准确（会话开始时间 vs 玩家加入时间）；后端无需改 | 玩家中途加入的场景，`started_at` 与 `joined_at` 有差异（但历史列表展示"这局什么时候打的"用 `started_at` 更合理） |
| B. 后端补 `JoinedAt` 字段 | `PlayerSessionBillRow` 新增 `JoinedAt`/`LeftAt`/`SeatNo`，SQL LEFT JOIN `session_players` 取这些字段 | 保留前端不变 | 增加 JOIN 开销；`joined_at` 语义不如 `started_at` 准确；后端需改 3 处（DTO + SQL + 转换函数） |
| C. 前端改用 `ended_at` | 用会话结束时间作为相对时间基准 | 适合"刚打完"的体感 | 进行中的会话 `ended_at = 0`，又会显示横线 |

### 2.3 推荐方案：A（前端改用 `started_at`）

**理由**：
1. `started_at`（会话开始时间）比 `joined_at`（玩家加入时间）语义更准确——历史列表展示的是"这局什么时候开始的"
2. `PlayerSessionBillRow` 已有 `StartedAt` 字段，后端无需改动
3. 改动范围最小，仅前端一行代码

**改动点**：[HistoryListItem.ts:160](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/widgets/history-list-item/HistoryListItem.ts#L160)

```typescript
// 修改前
text: i18n.t('history.list.time_ago', formatTimeAgo(item.joined_at ?? 0)),

// 修改后
text: i18n.t('history.list.time_ago', formatTimeAgo(item.started_at ?? 0)),
```

---

## 三、问题二：详情页缺少开始/结束时间

### 3.1 现状

当前详情页 [HistoryDetailScreen.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/HistoryDetailScreen.ts) 布局：

```
┌─────────────────────────────────────────────┐
│              房间 12345                      │ ← 顶栏标题（第 420 行）
│                                  [金币][音量] │
├─────────────────────────────────────────────┤
│  我的盈亏                          +120      │ ← 个人结果卡片（CARD_H=150）
│  抢到 80                            发出 50  │
├─────────────────────────────────────────────┤
│  回合明细                                    │ ← 回合标题
│  ┌─────────────────────────────────────┐    │
│  │ 第1局  玩家发包    抢到 30  红包总额 │    │ ← 回合列表
│  └─────────────────────────────────────┘    │
│  ...                                        │
└─────────────────────────────────────────────┘
```

**没有任何时间信息**（会话开始/结束时间、耗时）。

### 3.2 数据可用性

后端 `SessionInfo` DTO（[history_dto.go:49-60](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/history_dto.go#L49-L60)）已返回 `started_at` / `ended_at`：

```go
type SessionInfo struct {
    SessionID    string         `json:"session_id"`
    RoomNo       string         `json:"room_no"`
    ...
    StartedAt    int64          `json:"started_at"`  // ← 已有
    EndedAt      int64          `json:"ended_at"`    // ← 已有
    EndReason    string         `json:"end_reason"`
}
```

前端 `types.ts` 的 `SessionInfo` 也已有这两个字段。`PlayerSessionDetailResp.session` 已包含这些数据，**后端无需任何改动**。

### 3.3 展示位置方案对比

| 方案 | 位置 | 优点 | 缺点 |
|------|------|------|------|
| **D. 顶栏标题下方** | 房间号下方加一行时间 | 最直观；不挤压卡片空间 | 顶栏垂直空间有限（TITLE_TOP 后紧接 CARD_TOP） |
| **E. 个人结果卡片内** | 卡片内新增一行时间 | 信息聚合在卡片内 | 卡片高度需增加（当前 CARD_H=150） |
| F. 回合标题旁 | "回合明细"右侧 | 不影响卡片 | 时间信息与回合列表关联性弱 |

### 3.4 推荐方案：D + E 组合

**方案 D（顶栏标题下方）**：在房间号标题下方增加一行小字，显示开始时间 ~ 结束时间（或耗时）。

**方案 E（个人结果卡片底部）**：在个人结果卡片底部增加一行，显示"开始时间 - 结束时间"。

综合考虑空间和信息密度，**推荐方案 D**：仅在新增标题下方一行小字展示时间，不增加卡片高度，改动最小。

### 3.5 时间格式设计

#### 3.5.1 展示内容

会话已结束（`ended_at > 0`）：
```
开始 2026-06-26 14:30 ~ 结束 2026-06-26 15:12
```
或简化为：
```
2026-06-26 14:30 ~ 15:12（42分钟）
```

会话进行中（`ended_at = 0`，理论上历史页不会出现，但兼容处理）：
```
2026-06-26 14:30 ~ 进行中
```

#### 3.5.2 格式化函数

需要新增一个时间格式化工具函数（当前 `formatTimeAgo` 只输出相对时间 `5m`/`3h`/`2d`，不适合详情页）。

建议在 `HistoryDetailScreen.ts` 内联或提取到 `primitives/` 目录：

```typescript
/**
 * 将毫秒时间戳格式化为 `YYYY-MM-DD HH:mm` 格式
 */
function formatDateTime(ts: number): string {
  if (!Number.isFinite(ts) || ts <= 0) return '—'
  const d = new Date(ts)
  const yyyy = d.getFullYear()
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  const min = String(d.getMinutes()).padStart(2, '0')
  return `${yyyy}-${mm}-${dd} ${hh}:${min}`
}

/**
 * 计算两个时间戳之间的耗时（分钟）
 */
function formatDuration(startTs: number, endTs: number): string {
  if (!Number.isFinite(startTs) || startTs <= 0) return ''
  if (!Number.isFinite(endTs) || endTs <= 0) return ''
  const diffMin = Math.floor((endTs - startTs) / 60000)
  if (diffMin < 60) return `${diffMin}分钟`
  const h = Math.floor(diffMin / 60)
  const m = diffMin % 60
  return m > 0 ? `${h}小时${m}分钟` : `${h}小时`
}
```

#### 3.5.3 同日简化逻辑

如果开始和结束是同一天，结束时间省略日期：
```
2026-06-26 14:30 ~ 15:12（42分钟）
```

如果跨天，完整显示：
```
2026-06-26 23:50 ~ 2026-06-27 00:15（25分钟）
```

### 3.6 i18n 设计

新增 i18n key（zh/en/es 三个语言文件）：

```typescript
// zh.ts
'history.detail.time_range': '开始 {0} ~ 结束 {1}',
'history.detail.time_range_with_duration': '{0} ~ {1}（{2}）',
'history.detail.duration_min': '{0}分钟',
'history.detail.duration_hour_min': '{0}小时{1}分钟',
'history.detail.duration_hour': '{0}小时',
'history.detail.in_progress': '进行中',

// en.ts
'history.detail.time_range': 'Start {0} ~ End {1}',
'history.detail.time_range_with_duration': '{0} ~ {1} ({2})',
'history.detail.duration_min': '{0} min',
'history.detail.duration_hour_min': '{0}h {1}min',
'history.detail.duration_hour': '{0}h',
'history.detail.in_progress': 'In progress',

// es.ts
'history.detail.time_range': 'Inicio {0} ~ Fin {1}',
'history.detail.time_range_with_duration': '{0} ~ {1} ({2})',
'history.detail.duration_min': '{0} min',
'history.detail.duration_hour_min': '{0}h {1}min',
'history.detail.duration_hour': '{0}h',
'history.detail.in_progress': 'En curso',
```

### 3.7 布局调整

当前布局常量（[HistoryDetailScreen.ts:47-60](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/HistoryDetailScreen.ts#L47-L60)）：

```typescript
const TITLE_TOP = COIN_WALLET_SCENE_TOP + 78 + 8  // = 140
const CARD_TOP = TITLE_TOP + TITLE_FONT + 26       // = 206
```

新增时间行后，需要将 `CARD_TOP` 下移约 30px（时间行 fontSize 18 + 间距 12）：

```typescript
const TIME_LINE_FONT = 18
const TIME_LINE_GAP = 12
const CARD_TOP = TITLE_TOP + TITLE_FONT + TIME_LINE_FONT + TIME_LINE_GAP + 26
// = 140 + 40 + 18 + 12 + 26 = 236（比原来下移 30px）
```

或者不修改常量，而是在 `applyLayout` 中动态计算时间行位置，将卡片位置下移。

### 3.8 渲染逻辑

在 `loadDetail` 成功回调中（[HistoryDetailScreen.ts:417-437](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/HistoryDetailScreen.ts#L417-L437)），设置时间行文本：

```typescript
// 新增：顶栏时间行
const session = resp.session
const startTime = formatDateTime(session.started_at ?? 0)
const endTime = session.ended_at > 0
  ? formatDateTime(session.ended_at)
  : i18n.t('history.detail.in_progress')
const duration = session.ended_at > 0
  ? formatDuration(session.started_at, session.ended_at)
  : ''

if (duration) {
  timeLineText.text = i18n.t('history.detail.time_range_with_duration', startTime, endTime, duration)
} else {
  timeLineText.text = i18n.t('history.detail.time_range', startTime, endTime)
}
```

---

## 四、改动清单总结

### 4.1 列表时间修复（方案 A）

| 文件 | 改动 |
|------|------|
| [HistoryListItem.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/widgets/history-list-item/HistoryListItem.ts) | 第 160 行 `item.joined_at` → `item.started_at` |

**改动量**：1 行

### 4.2 详情页时间显示（方案 D）

| 文件 | 改动 |
|------|------|
| [HistoryDetailScreen.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/history/HistoryDetailScreen.ts) | 1. 新增 `timeLineText` Text 节点（房间号下方）<br>2. 新增 `formatDateTime` / `formatDuration` 工具函数<br>3. `loadDetail` 中设置时间行文本<br>4. `applyLayout` 中调整 `CARD_TOP` 下移<br>5. `destroy` 中销毁 `timeLineText` |
| [zh.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/core/systems/i18n/locales/zh.ts) | 新增 6 个 i18n key |
| [en.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/core/systems/i18n/locales/en.ts) | 新增 6 个 i18n key |
| [es.ts](file:///Users/aaron.pan/Desktop/party/gogain/packages/gift-box/src/core/systems/i18n/locales/es.ts) | 新增 6 个 i18n key |

**改动量**：约 60 行（含 i18n）

### 4.3 后端改动

**无**。`SessionInfo` 已包含 `started_at` / `ended_at`，`PlayerSessionBillRow` 已包含 `StartedAt` / `EndedAt`。

---

## 五、风险与注意事项

| 风险 | 影响 | 对策 |
|------|------|------|
| `CARD_TOP` 下移导致列表可视区域缩小 | 回合列表少显示约 1 行 | `LIST_BOTTOM_INSET` 可适当减小，或保持不变（列表本身可滚动） |
| 时区问题 | `new Date(ts).getHours()` 使用本地时区 | 前端按玩家本地时区显示，符合预期 |
| `ended_at = 0` 的进行中会话 | 时间行显示"进行中" | 历史列表仅查询 `gs.status = 1`（已完成），理论上不会出现，但兼容处理 |
| i18n key 命名冲突 | 与现有 key 重复 | 已检查，`history.detail.time_range` 等均为新 key |
| 同日简化逻辑增加复杂度 | 跨天判断 | 可选实现，初版可不做简化，统一显示完整日期 |

---

## 六、可选增强（未来考虑）

1. **列表项也显示绝对时间**：当前列表项只显示相对时间（`5m前`），可在 hover/长按时显示绝对时间 tooltip
2. **详情页回合列表显示每轮时间**：`RoundDetail` 已有 `started_at` / `ended_at`，可在 `HistoryRoundItem` 中展示每轮耗时
3. **耗时统计**：在统计卡片中新增"总游戏时长"字段
4. **时间筛选**：列表页支持按时间范围筛选（后端 `ListPlayerSessionsWithBill` 已支持 `startTime`/`endTime` 参数）
