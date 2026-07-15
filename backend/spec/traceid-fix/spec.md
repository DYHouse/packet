# 修复罚款扣款 traceID 缺失维度

## 问题背景

`GeneratePenaltyDeductTraceID` 当前仅基于 `roomID + sessionID` 生成 traceID，缺失 `userID` 和 `roundNo` 维度。这导致同一 session 内两个 bug：

### Bug 1：多玩家罚款 — platformBill 唯一索引冲突

同一 session 内多个玩家依次触发发包超时罚款时，所有罚款的 traceID 完全相同（`PENALTY_DED_{roomID}_{sessionID}`）。由于 platformBill 的 `UserID` 都是 `PlatformAccountID`，DB 唯一索引 `(round_trace_id, bill_type, user_id)` 冲突，导致事务回滚、扣款失败、`HandleDeductFailure` 错误终止游戏。

### Bug 2：同一玩家多次罚款 — 幂等检查错误跳过

同一玩家在同一 session 内多次触发罚款时（`kickRequired=false` 场景），第二次罚款的 traceID 与第一次完全相同，`GetBillByTraceTypeAndUser` 幂等检查命中第一次的 Success bill，静默跳过扣款。Redis 侧已扣金额但 platform 侧未扣，导致资金不一致。

## 根因

- 文件：`settlement/service/trace_id_generator.go:55-57`
- 当前签名：`GeneratePenaltyDeductTraceID(roomID, sessionID int64) string`
- 当前格式：`PENALTY_DED_%d_%d`（roomID, sessionID）
- 缺失维度：`userID`（区分不同玩家）、`roundNo`（区分同一玩家不同轮次的罚款）

## 修复方案

### 核心改动

将 `GeneratePenaltyDeductTraceID` 签名扩展为包含 `userID` 和 `roundNo`：

```
PENALTY_DED_{roomID}_{sessionID}_{userID}_{roundNo}
```

### 选择 roundNo 而非 penaltyCount 的理由

1. **RoundNo 已在 PenaltyDeductRequest 中存在**，无需新增 DTO 字段
2. **RoundNo 单调递增**：发包时 Lua `HMSET current_round roundNo`，回合结束只 `HDEL current_round_id` 不清 `current_round`，保证同一玩家连续罚款的 RoundNo 必不同
3. **业务语义更强**：直接对应业务轮次，便于追溯和对账
4. **改动更少**：仅改 traceID 生成函数 + 1 处调用，无需改 DTO 和调用链

## 影响范围

### 需修改的文件

| 文件 | 改动 |
|------|------|
| `settlement/service/trace_id_generator.go` | 修改 `GeneratePenaltyDeductTraceID` 签名和格式 |
| `settlement/service/penalty_settlement_service.go:72` | 更新调用处传入 `req.UserID` 和 `int64(req.RoundNo)` |
| `settlement/service/trace_id_generator_test.go` | 新增 `GeneratePenaltyDeductTraceID` 测试用例 |

### 不需修改的文件

- `settlement/dto/request.go` — `PenaltyDeductRequest` 已有 `UserID` 和 `RoundNo` 字段
- `game/application/penalty_service.go` — 不涉及 traceID 生成
- `game/application/game_lifecycle_timeout.go` — 不涉及 traceID 生成
- `settlement/service/penalty_settlement_service.go` 的 `DistributePenaltyFromPlatform` — 罚款分配 traceID 不改（整个 session 只调用一次，无冲突风险）

### 不受影响的能力

- **幂等性**：仍由 `(round_trace_id, bill_type, user_id)` DB 唯一索引兜底，traceID 格式变化不影响幂等机制
- **确定性**：同一罚款事件的 `(roomID, sessionID, userID, roundNo)` 固定，Kafka 重试可复现
- **历史数据**：已存在的旧格式 traceID bill 不受影响，仅新 bill 使用新格式
- **查询方法**：`GetBillByTraceTypeAndUser` 无需改动（查询条件不变）

## 验证场景

### 场景 1：同一玩家多次罚款

| 步骤 | RoundNo | traceID | 幂等检查 | 预期结果 |
|------|---------|---------|----------|----------|
| 第 1 次 | N-1 | `PENALTY_DED_1_100_A_{N-1}` | nil → 创建 bill | 扣款成功 |
| 第 2 次 | N | `PENALTY_DED_1_100_A_N` | nil → 创建 bill | 扣款成功 |

### 场景 2：多玩家罚款

| 步骤 | 玩家 | RoundNo | traceID | 预期结果 |
|------|------|---------|---------|----------|
| A 罚款 | A | N-1 | `PENALTY_DED_1_100_A_{N-1}` | 扣款成功 |
| B 罚款 | B | N-1 | `PENALTY_DED_1_100_B_{N-1}` | 扣款成功 |

### 场景 3：多玩家同轮罚款（极端情况）

| 步骤 | 玩家 | RoundNo | traceID | 预期结果 |
|------|------|---------|---------|----------|
| A 罚款 | A | N-1 | `PENALTY_DED_1_100_A_{N-1}` | 扣款成功 |
| B 罚款 | B | N-1 | `PENALTY_DED_1_100_B_{N-1}` | 扣款成功 |

UserID 不同 → traceID 不同 → 无冲突。

### 场景 4：Kafka 重试幂等

| 步骤 | traceID | 幂等检查 | 预期结果 |
|------|---------|----------|----------|
| 首次处理 | `PENALTY_DED_1_100_A_5` | nil → 创建 bill | 扣款成功 |
| Kafka 重试 | `PENALTY_DED_1_100_A_5`（相同输入） | 命中 Success bill → 跳过 | 不重复扣款 |
