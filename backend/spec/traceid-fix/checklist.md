# 验证清单

## 代码改动验证

- [ ] `GeneratePenaltyDeductTraceID` 签名改为 `(roomID, sessionID, userID, roundNo int64) string`
- [ ] 格式改为 `PENALTY_DED_%d_%d_%d_%d`（roomID, sessionID, userID, roundNo）
- [ ] `penalty_settlement_service.go:72` 调用处传入 `req.UserID, int64(req.RoundNo)`
- [ ] `GeneratePenaltyDistTraceID` 不改动（罚款分配无此问题）
- [ ] 无其他调用 `GeneratePenaltyDeductTraceID` 的地方被遗漏

## 编译验证

- [ ] `go build ./settlement/...` 通过
- [ ] `go vet ./settlement/service/...` 通过

## 单元测试验证

- [ ] 新增 `TestTraceIDGenerator_GeneratePenaltyDeductTraceID` 测试确定性（相同输入相同输出）
- [ ] 测试覆盖多玩家场景（不同 userID 产生不同 traceID）
- [ ] 测试覆盖同玩家多次罚款场景（不同 roundNo 产生不同 traceID）
- [ ] 测试覆盖 roundNo=0 边界情况
- [ ] `go test ./settlement/service/... -count=1` 全部通过

## 语义验证

- [ ] traceID 包含 userID 维度，多玩家罚款不再冲突
- [ ] traceID 包含 roundNo 维度，同玩家多次罚款不再被错误跳过
- [ ] traceID 保持确定性，Kafka 重试可复现
- [ ] DB 唯一索引 `(round_trace_id, bill_type, user_id)` 仍为幂等兜底
- [ ] 罚款分配（`GeneratePenaltyDistTraceID`）不受影响
