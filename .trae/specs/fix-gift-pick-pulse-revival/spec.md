# Fix Gift Pick Pulse Revival Spec

## Why

抢红包阶段存在脉冲动效异常唤醒 bug：本机玩家抢完红包后，金色光晕 + 呼吸脉冲动画会消失（符合预期），但当收到其他玩家（尤其机器人，因 1-8s 延迟更易落在本机抢之后）的 `packet_grabbed` 推送时，未抢座位的脉冲动效会**重新亮起**，造成误导用户以为还能抢的视觉错误。

### 根因

`RoomScene.ts` 仅用布尔变量 `grabLocalGrabbed` 跟踪本机是否已抢，未持久化本机抢到的座位索引。每次 `packet_grabbed` 推送时临时计算 `localGrabSeatIndex`：

```typescript
// RoomScene.ts:1232
localGrabSeatIndex: selfGrab ? pos : null,
```

当非本机玩家抢红包时 `selfGrab = false`，`localGrabSeatIndex` 被传为 `null`，经由 `updateGrabRound` → `setGiftPickAvatarMode(true, null, …)` 把 `RoomCentralTable.localGiftPickSeatIndex` 重置为 `null`，导致：
- `alreadyLocalGrabbed = localGiftPickSeatIndex !== null` 变回 `false`
- 未抢座位 `isClickable = giftPickInteractive && !isTakenByServer && !alreadyLocalGrabbed` 变回 `true`
- `rebuildSeats()` 重建座位时为未抢座位重新挂上金色光晕 + 呼吸脉冲 Ticker

### 为什么机器人比真人更容易触发

真人通常几百毫秒内抢（本机抢之前），动效本来就亮，看不出唤醒；机器人有 1-8s 延迟（`grab_delay_min: 1s` / `grab_delay_max: 8s`），大概率落在本机抢之后，动效已消失，被重置后重新亮起，表现为"唤醒"。

## What Changes

### 方案 A：在 RoomScene 持久化本机抢座位索引

仅修改 `gogain/packages/gift-box/src/scene/RoomScene.ts`，不改 `RoomScreen.ts` / `RoomCentralTable.ts` 的 API 契约。

1. 新增变量 `let grabLocalSeatIndex: number | null = null`（在现有 `grabLocalGrabbed` 附近）
2. 本机乐观抢包时（`onGiftPickSeatRequest` 回调内）写入 `grabLocalSeatIndex`
3. `packet_grabbed` 推送中 `selfGrab` 时写入 `grabLocalSeatIndex`
4. 所有 `updateGrabRound` 调用统一用 `grabLocalSeatIndex` 替代临时计算值
5. `round_start` 和 `game_start` 重置点同步清空 `grabLocalSeatIndex = null`

### 明确不做

- 不修改 `RoomScreen.ts` / `RoomCentralTable.ts`：保持现有 API 契约
- 不修改 `buildGiftSeat` 的脉冲动效挂载逻辑：`isClickable` 判断逻辑本身正确
- 不修改 `showTapGuide` 涟漪引导：由 `localStorage('gift_pick_guided')` 永久标记控制，与本 bug 无关
- 不修改后端机器人调度逻辑：`grab_skip_prob` 跳过属正常业务逻辑（用户已确认）

## Impact

- Affected specs: 无（首次针对 gogain 前端的修复 spec）
- Affected code:
  - `gogain/packages/gift-box/src/scene/RoomScene.ts`：新增 1 个变量 + 6 处赋值/传参修改
- 不影响下一轮动效引导：`round_start` 时 `enterGrabRound` → `setGiftPickAvatarMode(true, null, …)` 本来就会重置 `RoomCentralTable.localGiftPickSeatIndex`，方案 A 的重置只是冗余保险
- 不影响涟漪引导：由 `localStorage` 控制，与本方案无关
