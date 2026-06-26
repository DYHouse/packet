# 单局详情页回合明细补充发出金额信息 Spec

## Why

当前单局详情页（HistoryDetailScreen）的回合明细条目（HistoryRoundItem）仅展示玩家的"抢到"金额，但当该局玩家有支出时（自己发红包、首局均摊房费），缺少"发出/出资"金额的展示，导致玩家无法在每局明细中直观看到自己的支出。

## 背景：游戏逻辑分析

通过阅读代码，确认了以下游戏规则和资金流向：

### 回合类型与玩家支出

| 回合类型 | sender_type | 玩家支出 | 说明 |
|---------|-------------|---------|------|
| 首局（第1轮） | system | 每人出资 `roomFee / playerCount` | 所有玩家均摊房费，系统统一发包 |
| 后续轮（我发包） | player（sender_id = 我） | 我出资 `totalAmount` | 上一轮我是手气最差者，本轮由我发包 |
| 后续轮（他人发包） | player（sender_id ≠ 我） | 无支出 | 我只是抢红包，不承担发包成本 |
| 特殊系统发包 | system_forced / system_resume | 无支出 | 超时/恢复场景，系统代发 |

### 关键代码逻辑

1. **首局扣费**：`initRoundAndDeduct` 中 `roomFeePerPlayer = meta.RoomFee / int64(len(players))`，从每个玩家余额扣除
2. **后续轮扣费**：`initLaterRoundAndDeduct` 中由 sender（上一轮 minPlayer）独付全额 `roomFee`
3. **minPlayer 机制**：每轮抢到最少金额的玩家成为 minPlayer，在 `session_players` 中 `total_send += totalAmount`。**首局虽然是系统发包，minPlayer 仍被计入 total_send**
4. **sender 也可抢自己的红包**：LuaGrabPacket 脚本不排除 sender 参与

### 当前 API 缺陷

`RoundDetail` 当前没有字段表示"我在该轮的支出金额"。需要新增 `my_send_amount` 字段，由后端计算：
- 首局（sender_type 为 system 且 round_no = 1）：`my_send_amount = roomFee / playerCount`
- 我发包（sender_id = userID 且 sender_type = player）：`my_send_amount = totalAmount`
- 其他情况：`my_send_amount = 0`

## What Changes

- **后端 RoundDetail DTO**：新增 `my_send_amount` 字段，由 `HistoryService.GetPlayerSessionDetail` 计算
- **后端 HistoryService**：在组装 RoundDetail 时计算 `my_send_amount`
- **前端 types.ts**：`RoundDetail` 新增 `my_send_amount: number` 字段
- **前端 HistoryRoundItem**：当 `my_send_amount > 0` 时，右下角显示"发出 {my_send_amount}"（红色），否则显示"红包总额 {total_amount}"（白色）
- **前端 i18n**：新增 `history.detail.send_amount_round` 国际化文案

## Impact

- Affected code:
  - `packet/backend/game/application/history_dto.go` — RoundDetail 新增字段
  - `packet/backend/game/application/history_service.go` — 计算 my_send_amount
  - `gogain/packages/gift-box/src/history/types.ts` — 前端类型
  - `gogain/packages/gift-box/src/history/widgets/history-round-item/HistoryRoundItem.ts` — 展示逻辑
  - `gogain/packages/gift-box/src/core/systems/i18n/locales/zh.ts` — 新增翻译
  - `gogain/packages/gift-box/src/core/systems/i18n/locales/en.ts` — 新增翻译
  - `gogain/packages/gift-box/src/core/systems/i18n/locales/es.ts` — 新增翻译
- **Breaking Change**: 无。`my_send_amount` 为新增可选字段，不影响现有客户端

## ADDED Requirements

### Requirement: 后端 RoundDetail 新增 my_send_amount 字段

`RoundDetail` 应新增 `my_send_amount` 字段，表示当前玩家在该轮的支出金额。

#### Scenario: 首局系统发包

- **GIVEN** 玩家查询单局详情
- **AND** 某轮 round_no = 1，sender_type 为 system
- **WHEN** 后端组装 RoundDetail
- **THEN** `my_send_amount = roomFee / playerCount`（均摊房费）
- **AND** `playerCount` 取自 `game_sessions` 表的 `player_count` 字段（由 session_start 事件写入）

#### Scenario: 玩家自己发包

- **GIVEN** 玩家查询单局详情
- **AND** 某轮 sender_id 等于当前用户 ID，sender_type 为 player
- **WHEN** 后端组装 RoundDetail
- **THEN** `my_send_amount = totalAmount`（全额发包成本）

#### Scenario: 其他玩家发包

- **GIVEN** 玩家查询单局详情
- **AND** 某轮 sender_id 不等于当前用户 ID，sender_type 为 player
- **WHEN** 后端组装 RoundDetail
- **THEN** `my_send_amount = 0`（无支出）

#### Scenario: 特殊系统发包（system_forced / system_resume）

- **GIVEN** 玩家查询单局详情
- **AND** 某轮 sender_type 为 system_forced 或 system_resume
- **WHEN** 后端组装 RoundDetail
- **THEN** `my_send_amount = 0`（系统代发，玩家无支出）

### Requirement: 前端 HistoryRoundItem 展示发出金额

当 `RoundDetail.my_send_amount > 0` 时，HistoryRoundItem 右下角应显示"发出 {my_send_amount}"（红色/橙色），否则保持现有"红包总额 {total_amount}"（白色）展示。

#### Scenario: 首局 — 我出资了且抢到了

- **GIVEN** 玩家查看单局详情页
- **AND** 首轮 my_send_amount > 0
- **AND** my_grab 存在（抢到了金额）
- **WHEN** 渲染该轮的 HistoryRoundItem
- **THEN** 左侧发包类型显示"系统发包"
- **AND** 右上角显示"抢到 {my_grab.amount}"（金色）
- **AND** 右下角显示"发出 {my_send_amount}"（红色/橙色，表示支出）

#### Scenario: 首局 — 我出资了但没抢到

- **GIVEN** 玩家查看单局详情页
- **AND** 首轮 my_send_amount > 0
- **AND** my_grab 不存在（未抢到）
- **WHEN** 渲染该轮的 HistoryRoundItem
- **THEN** 左侧发包类型显示"系统发包"
- **AND** 右上角显示"未抢到"（灰色）
- **AND** 右下角显示"发出 {my_send_amount}"（红色/橙色，表示支出）

#### Scenario: 我发包且抢到了红包

- **GIVEN** 玩家查看单局详情页
- **AND** 某轮 sender_id 等于当前用户 ID，sender_type 为 player
- **AND** my_send_amount = total_amount > 0
- **AND** my_grab 存在
- **WHEN** 渲染该轮的 HistoryRoundItem
- **THEN** 左侧发包类型显示"我发包"
- **AND** 右上角显示"抢到 {my_grab.amount}"（金色）
- **AND** 右下角显示"发出 {my_send_amount}"（红色/橙色，表示支出）

#### Scenario: 我发包但没抢到红包

- **GIVEN** 玩家查看单局详情页
- **AND** 某轮 sender_id 等于当前用户 ID，sender_type 为 player
- **AND** my_send_amount = total_amount > 0
- **AND** my_grab 不存在
- **WHEN** 渲染该轮的 HistoryRoundItem
- **THEN** 左侧发包类型显示"我发包"
- **AND** 右上角显示"未抢到"（灰色）
- **AND** 右下角显示"发出 {my_send_amount}"（红色/橙色，表示支出）

#### Scenario: 他人发包 — 无支出

- **GIVEN** 玩家查看单局详情页
- **AND** 某轮 my_send_amount = 0
- **WHEN** 渲染该轮的 HistoryRoundItem
- **THEN** 保持现有行为：右上角显示抢到/未抢到，右下角显示"红包总额 {total_amount}"（白色）

#### Scenario: 特殊系统发包（system_forced / system_resume）

- **GIVEN** 玩家查看单局详情页
- **AND** 某轮 sender_type 为 system_forced / system_resume
- **AND** my_send_amount = 0
- **WHEN** 渲染该轮的 HistoryRoundItem
- **THEN** 保持现有行为：右上角显示抢到/未抢到，右下角显示"红包总额 {total_amount}"（白色）
