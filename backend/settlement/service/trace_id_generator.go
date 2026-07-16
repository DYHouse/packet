package service

import (
	"fmt"

	"github.com/cashparty/backend/common/idgen"
)

// TraceIDGenerator 业务订单号/TraceID 生成器。
// 依赖 IDGenerator 接口（非具体类型），便于 mock 测试（规约 SID-10）。
type TraceIDGenerator struct {
	idGen idgen.IDGenerator
}

// NewTraceIDGenerator 创建 TraceIDGenerator。
// idGen 必须非 nil，调用方负责在 bootstrap 层注入已初始化的 IDGenerator。
func NewTraceIDGenerator(idGen idgen.IDGenerator) *TraceIDGenerator {
	return &TraceIDGenerator{idGen: idGen}
}

// GenerateRoundTraceID 基于 sessionID+roundNo 确定性生成 roundTraceID。
// 重试时可复现，作为 BizOrderNo 的基础。
func (g *TraceIDGenerator) GenerateRoundTraceID(sessionID int64, roundNo int) string {
	return fmt.Sprintf("RT_%d_%d", sessionID, roundNo)
}

// GenerateBizOrderNo 基于业务语义确定性生成，重试时可复现，便于平台基于 BizID 做幂等去重。
func (g *TraceIDGenerator) GenerateBizOrderNo(roundTraceID string, billType int, userID int64) string {
	return fmt.Sprintf("%s_%d_%d", roundTraceID, billType, userID)
}

// GenerateBatchID 生成扣款批次 ID。
// 非确定性，使用雪花 ID。返回 (string, error)，调用方 MUST 检查 error（规约 SID-C4）。
func (g *TraceIDGenerator) GenerateBatchID() (string, error) {
	id, err := g.idGen.GenerateInt64()
	if err != nil {
		return "", fmt.Errorf("generate batch id: %w", err)
	}
	return fmt.Sprintf("BATCH_%d", id), nil
}

// GenerateRefundOrderNo 退款订单号基于 billID 确定性生成，重试时可复现。
func (g *TraceIDGenerator) GenerateRefundOrderNo(billID int64) string {
	return fmt.Sprintf("REFUND_%d", billID)
}

// GenerateExceptionNo 基于 billID + exceptionType 确定性生成异常单号。
// 相同输入始终产生相同输出，支持幂等重试（重试时同一 bill + type 生成相同 ExceptionNo，
// 通过唯一索引/Exists 检查可识别为重复创建，跳过）。
// 禁止使用时间戳 + 随机数（非确定性，破坏幂等）。
func (g *TraceIDGenerator) GenerateExceptionNo(billID int64, exceptionType string) string {
	return fmt.Sprintf("EXC_%d_%s", billID, exceptionType)
}

// GeneratePenaltyDeductTraceID 基于房间+会话+用户+轮次确定性生成罚款扣款 traceID。
// 包含 userID 维度以区分不同玩家的罚款，包含 roundNo 维度以区分同一玩家不同轮次的罚款。
// 重试时可复现，作为 BizOrderNo 的基础。
func (g *TraceIDGenerator) GeneratePenaltyDeductTraceID(roomID, sessionID, userID, roundNo int64) string {
	return fmt.Sprintf("PENALTY_DED_%d_%d_%d_%d", roomID, sessionID, userID, roundNo)
}

// GeneratePenaltyDistTraceID 基于房间+会话+轮次确定性生成罚款分发 traceID。
// 包含 roundNo 维度以提升可追溯性，便于按 round 维度定位分发账目。
// 重试时可复现，作为 BizOrderNo 的基础。
func (g *TraceIDGenerator) GeneratePenaltyDistTraceID(roomID, sessionID int64, roundNo int) string {
	return fmt.Sprintf("PENALTY_DIST_%d_%d_%d", roomID, sessionID, roundNo)
}

// GenerateGameSettleTraceID 游戏结算 traceID（基于 sessionID 确定性生成，重试时可复现）。
func (g *TraceIDGenerator) GenerateGameSettleTraceID(sessionID int64) string {
	return fmt.Sprintf("GAME_SETTLE_%d", sessionID)
}

// GenerateSessionCreditTraceID 会话级入账 traceID（基于 sessionID+userID 确定性生成，重试时可复现）。
func (g *TraceIDGenerator) GenerateSessionCreditTraceID(sessionID int64, userID int64) string {
	return fmt.Sprintf("SESSION_CREDIT_%d_%d", sessionID, userID)
}
