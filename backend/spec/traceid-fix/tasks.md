# 实施任务

## 任务 1：修改 `GeneratePenaltyDeductTraceID` 签名和格式

**文件**：`settlement/service/trace_id_generator.go`

**改动**：
- 签名从 `GeneratePenaltyDeductTraceID(roomID, sessionID int64) string` 改为 `GeneratePenaltyDeductTraceID(roomID, sessionID, userID, roundNo int64) string`
- 格式从 `PENALTY_DED_%d_%d` 改为 `PENALTY_DED_%d_%d_%d_%d`
- 新增中文 godoc 注释，说明各维度用途

## 任务 2：更新调用处

**文件**：`settlement/service/penalty_settlement_service.go`

**改动**：
- 第 72 行调用从 `GeneratePenaltyDeductTraceID(req.RoomID, req.SessionID)` 改为 `GeneratePenaltyDeductTraceID(req.RoomID, req.SessionID, req.UserID, int64(req.RoundNo))`

## 任务 3：新增单元测试

**文件**：`settlement/service/trace_id_generator_test.go`

**改动**：
- 新增 `TestTraceIDGenerator_GeneratePenaltyDeductTraceID` 测试函数
- 覆盖用例：
  - 确定性：相同输入产生相同输出
  - 多玩家：不同 userID 产生不同 traceID
  - 同玩家多次罚款：不同 roundNo 产生不同 traceID
  - 边界：roundNo=0、userID=0

## 任务 4：构建和测试验证

- `go build ./settlement/...`
- `go vet ./settlement/service/...`
- `go test ./settlement/service/... -count=1 -timeout 120s`
