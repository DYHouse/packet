# Tasks

## Phase 1: 修复脉冲动效异常唤醒

- [ ] Task 1: 在 RoomScene.ts 新增 `grabLocalSeatIndex` 变量并持久化本机抢座位索引
  - [ ] SubTask 1.1: 在 `let grabLocalGrabbed = false`（line 540 附近）下方新增 `let grabLocalSeatIndex: number | null = null`
  - [ ] SubTask 1.2: 修改 `onGiftPickSeatRequest` 乐观抢包路径（line 484 附近）：在 `grabLocalGrabbed = true` 后追加 `grabLocalSeatIndex = pos >= 0 && pos < 5 ? pos : null`
  - [ ] SubTask 1.3: 修改 `onGiftPickSeatRequest` 的 `updateGrabRound` 调用（line 489）：`localGrabSeatIndex: grabLocalSeatIndex`（替代 `pos >= 0 && pos < 5 ? pos : null`）
  - [ ] SubTask 1.4: 修改 `packet_grabbed` 推送处理（line 1227 附近）：在 `if (selfGrab) grabLocalGrabbed = true` 后追加 `if (selfGrab) grabLocalSeatIndex = pos >= 0 && pos < 5 ? pos : null`
  - [ ] SubTask 1.5: 修改 `packet_grabbed` 推送的 `updateGrabRound` 调用（line 1232）：`localGrabSeatIndex: grabLocalSeatIndex`（替代 `selfGrab ? pos : null`）

- [ ] Task 2: 在重置点同步清空 `grabLocalSeatIndex`
  - [ ] SubTask 2.1: `game_start` 重置块（line 1016 附近）：在 `grabLocalGrabbed = false` 后追加 `grabLocalSeatIndex = null`
  - [ ] SubTask 2.2: `round_start` 重置块（line 1308 附近）：在 `grabLocalGrabbed = false` 后追加 `grabLocalSeatIndex = null`

- [ ] Task 3: 验证
  - [ ] SubTask 3.1: TypeScript 编译通过（`pnpm --filter gift-box build` 或 `tsc --noEmit`）
  - [ ] SubTask 3.2: 代码审查确认所有 `updateGrabRound` 调用点均已改用 `grabLocalSeatIndex`
