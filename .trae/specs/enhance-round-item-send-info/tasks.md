# Tasks

- [ ] Task 1: 后端 RoundDetail DTO 新增 my_send_amount 字段
  - [ ] SubTask 1.1: 在 `history_dto.go` 的 `RoundDetail` 结构体中新增 `MySendAmount currency.Money` 字段（json: `my_send_amount`）
  - [ ] SubTask 1.2: 在 `history_service.go` 的 `GetPlayerSessionDetail` 方法中计算 `MySendAmount`：
    - 首局（round_no = 1 且 sender_type 为 system）：`my_send_amount = session.RoomFee / session.PlayerCount`
    - 我发包（sender_id == userID 且 sender_type 为 player）：`my_send_amount = round.TotalAmount`
    - 其他情况：`my_send_amount = 0`

- [ ] Task 2: 前端类型与展示逻辑更新
  - [ ] SubTask 2.1: 在 `types.ts` 的 `RoundDetail` 中新增 `my_send_amount: number` 字段
  - [ ] SubTask 2.2: 在 `HistoryRoundItem.ts` 中，当 `round.my_send_amount > 0` 时，右下角显示"发出 {my_send_amount}"（红色 0xe85050），否则保持"红包总额 {total_amount}"（白色）
  - [ ] SubTask 2.3: `HistoryRoundItem` 不再需要 `currentUserId` 参数来判断"我发包"场景，改为直接依赖 `my_send_amount > 0`（但保留 `currentUserId` 用于左侧"我发包"标签判断）

- [ ] Task 3: 新增 i18n 翻译 key
  - [ ] SubTask 3.1: 在 `zh.ts` 中新增 `history.detail.send_amount_round: '发出 {0}'`
  - [ ] SubTask 3.2: 在 `en.ts` 中新增 `history.detail.send_amount_round: 'Sent {0}'`
  - [ ] SubTask 3.3: 在 `es.ts` 中新增 `history.detail.send_amount_round: 'Enviado {0}'`

# Task Dependencies
- Task 2 依赖 Task 1（需要后端返回 my_send_amount 字段）
- Task 3 应与 Task 2 同步完成
