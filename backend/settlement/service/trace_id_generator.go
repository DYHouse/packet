package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

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

// GenerateReconcileNo 生成对账单号。
// 使用 crypto/rand 生成 4 位随机数，避免雪花 ID 低位 sequence 碰撞（规约 SID-12）。
// 返回 (string, error)，调用方 MUST 检查 error。
func (g *TraceIDGenerator) GenerateReconcileNo() (string, error) {
	timestamp := time.Now().Format("20060102150405")
	random, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", fmt.Errorf("generate reconcile no: %w", err)
	}
	return fmt.Sprintf("REC_%s_%04d", timestamp, random.Int64()), nil
}

// GenerateExceptionNo 基于 billID + exceptionType 确定性生成异常单号。
// 相同输入始终产生相同输出，支持幂等重试（重试时同一 bill + type 生成相同 ExceptionNo，
// 通过唯一索引/Exists 检查可识别为重复创建，跳过）。
// 禁止使用时间戳 + 随机数（非确定性，破坏幂等）。
func (g *TraceIDGenerator) GenerateExceptionNo(billID int64, exceptionType string) string {
	return fmt.Sprintf("EXC_%d_%s", billID, exceptionType)
}

func (g *TraceIDGenerator) GeneratePenaltyDeductTraceID(roomID, sessionID int64) string {
	return fmt.Sprintf("PENALTY_DED_%d_%d", roomID, sessionID)
}

func (g *TraceIDGenerator) GeneratePenaltyDistTraceID(roomID, sessionID int64) string {
	return fmt.Sprintf("PENALTY_DIST_%d_%d", roomID, sessionID)
}

// GenerateGameSettleTraceID 游戏结算 traceID（基于 sessionID 确定性生成，重试时可复现）。
func (g *TraceIDGenerator) GenerateGameSettleTraceID(sessionID int64) string {
	return fmt.Sprintf("GAME_SETTLE_%d", sessionID)
}

// GenerateSessionCreditTraceID 会话级入账 traceID（基于 sessionID+userID 确定性生成，重试时可复现）。
func (g *TraceIDGenerator) GenerateSessionCreditTraceID(sessionID int64, userID int64) string {
	return fmt.Sprintf("SESSION_CREDIT_%d_%d", sessionID, userID)
}

func ParseRoundTraceID(roundTraceID string) (sessionID int64, roundNo int, err error) {
	parts := strings.Split(roundTraceID, "_")
	if len(parts) != 3 || parts[0] != "RT" {
		return 0, 0, fmt.Errorf("invalid round_trace_id format: %s", roundTraceID)
	}

	sessionID, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid session_id in round_trace_id: %w", err)
	}

	roundNo, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid round_no in round_trace_id: %w", err)
	}

	return sessionID, roundNo, nil
}

func ExtractSessionIDFromRoundTraceID(roundTraceID string) int64 {
	sessionID, _, err := ParseRoundTraceID(roundTraceID)
	if err != nil {
		return 0
	}
	return sessionID
}
