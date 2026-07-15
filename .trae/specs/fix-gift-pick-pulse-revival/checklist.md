# Verification Checklist

## Phase 1: 修复脉冲动效异常唤醒

### 变量持久化
- [ ] `RoomScene.ts` 新增 `let grabLocalSeatIndex: number | null = null` 变量
- [ ] `onGiftPickSeatRequest` 乐观抢包成功后写入 `grabLocalSeatIndex`
- [ ] `packet_grabbed` 推送中 `selfGrab` 时写入 `grabLocalSeatIndex`

### updateGrabRound 调用统一
- [ ] `onGiftPickSeatRequest` 内的 `updateGrabRound` 调用使用 `grabLocalSeatIndex`（不再临时计算）
- [ ] `packet_grabbed` 推送内的 `updateGrabRound` 调用使用 `grabLocalSeatIndex`（不再用 `selfGrab ? pos : null`）
- [ ] 全文件 grep `localGrabSeatIndex:` 确认所有调用点均已统一（应只出现 `localGrabSeatIndex: grabLocalSeatIndex`）

### 重置点同步
- [ ] `game_start` 重置块包含 `grabLocalSeatIndex = null`
- [ ] `round_start` 重置块包含 `grabLocalSeatIndex = null`

### 功能验证
- [ ] TypeScript 编译通过，无类型错误
- [ ] 本机抢红包后，脉冲动效消失（符合预期）
- [ ] 本机抢完后，收到其他玩家（真人或机器人）`packet_grabbed` 推送时，脉冲动效**不再重新亮起**
- [ ] 未抢座位被其他玩家抢后，该座位的脉冲动效正确消失（头像叠上，不可再点）
- [ ] 下一轮 `round_start` 时，所有座位的脉冲动效正常亮起（不受上一轮状态影响）
- [ ] 首局涟漪引导（`showTapGuide`）行为不变：首局本机抢完后不再触发，后续轮次也不触发

### 边界场景
- [ ] 本机抢包 WS 响应 `pos` 越界（<0 或 >=5）时 `grabLocalSeatIndex` 为 `null`，不崩溃
- [ ] `packet_grabbed` 推送 `pos` 越界时 `grabLocalSeatIndex` 保持原值（仅 `selfGrab` 时才赋值），不崩溃
- [ ] 观众位 `spectator=true` 时不受影响（`onGiftPickSeatRequest` 内 `isLocalSpectator` 提前 return）
