package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cashparty/backend/common/idgen"
)

type TraceIDGenerator struct {
	idGen *idgen.SnowflakeGenerator
}

func NewTraceIDGenerator(idGen *idgen.SnowflakeGenerator) *TraceIDGenerator {
	return &TraceIDGenerator{idGen: idGen}
}

func (g *TraceIDGenerator) GenerateRoundTraceID(sessionID int64, roundNo int) string {
	return fmt.Sprintf("RT_%d_%d", sessionID, roundNo)
}

// GenerateBizOrderNo 基于业务语义确定性生成，重试时可复现，便于平台基于 BizID 做幂等去重
func (g *TraceIDGenerator) GenerateBizOrderNo(roundTraceID string, billType int, userID int64) string {
	return fmt.Sprintf("%s_%d_%d", roundTraceID, billType, userID)
}

func (g *TraceIDGenerator) GenerateBatchID() string {
	return fmt.Sprintf("BATCH_%d", g.idGen.GenerateInt64())
}

// GenerateRefundOrderNo 退款订单号基于 billID 确定性生成，重试时可复现
func (g *TraceIDGenerator) GenerateRefundOrderNo(billID int64) string {
	return fmt.Sprintf("REFUND_%d", billID)
}

func (g *TraceIDGenerator) GenerateReconcileNo() string {
	timestamp := time.Now().Format("20060102150405")
	random := g.idGen.GenerateInt64() % 10000
	return fmt.Sprintf("REC_%s_%04d", timestamp, random)
}

func (g *TraceIDGenerator) GenerateExceptionNo() string {
	timestamp := time.Now().Format("20060102150405")
	random := g.idGen.GenerateInt64() % 10000
	return fmt.Sprintf("EXC_%s_%04d", timestamp, random)
}

func (g *TraceIDGenerator) GeneratePenaltyDeductTraceID(roomID, sessionID int64) string {
	return fmt.Sprintf("PENALTY_DED_%d_%d", roomID, sessionID)
}

func (g *TraceIDGenerator) GeneratePenaltyDistTraceID(roomID, sessionID int64) string {
	return fmt.Sprintf("PENALTY_DIST_%d_%d", roomID, sessionID)
}

// GenerateGameSettleTraceID 游戏结算 traceID（基于 sessionID 确定性生成，重试时可复现）
func (g *TraceIDGenerator) GenerateGameSettleTraceID(sessionID int64) string {
	return fmt.Sprintf("GAME_SETTLE_%d", sessionID)
}

// GenerateSessionCreditTraceID 会话级入账 traceID（基于 sessionID+userID 确定性生成，重试时可复现）
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
