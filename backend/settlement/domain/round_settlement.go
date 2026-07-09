package domain

import (
	"errors"
	"fmt"
	"time"
)

// RoundStatus 回合结算状态枚举值，表示单局结算的生命周期阶段。
// 状态流转：
//
//	Deducting(0) → Deducted(1) / Failed(5)
//	Deducted(1) → Credited(6) / Failed(5)
//	Credited(6) / Failed(5) 为终态。
const (
	RoundStatusDeducting = 0
	RoundStatusDeducted  = 1
	RoundStatusFailed    = 5
	RoundStatusCredited  = 6
)

// RoundSettlement 回合结算聚合根，表示一局游戏的扣款与派奖结算汇总记录。
// 纯领域类型，无 GORM tag 与 TableName 方法；持久化由 model.RoundSettlement 承载，
// Repository 实现层负责 domain ↔ model 转换。
type RoundSettlement struct {
	ID                 int64
	RoundTraceID       string
	RoomID             int64
	SessionID          int64
	RoundID            int64
	RoundNo            int
	DeductScene        int
	DeductAmount       int64
	DeductUserCount    int
	DeductSuccessCount int
	DeductedAt         *time.Time
	SettleAmount       int64
	SettleUserCount    int
	SettleSuccessCount int
	SettledAt          *time.Time
	SenderID           int64
	SenderType         string
	TotalAmount        int64
	Commission         int64
	PlayerCount        int
	MinPlayerID        int64
	RewardType         int
	RewardAmount       int64
	Status             int
	ReconcileStatus    int
	RefundStatus       int
	RefundReason       string
	ErrorMessage       string
	GameSettleStatus   int
	GameSettledAt      *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CanSettle 校验当前回合结算是否可进入派奖阶段。
// 仅 Deducted 状态（扣款完成）允许进入派奖；其他状态拒绝。
func (r *RoundSettlement) CanSettle() bool {
	return r.Status == RoundStatusDeducted
}

// IsTerminalStatus 校验当前回合结算是否处于终态（不再发生状态流转）。
// Credited 与 Failed 为终态。
func (r *RoundSettlement) IsTerminalStatus() bool {
	return r.Status == RoundStatusCredited || r.Status == RoundStatusFailed
}

// TransitionTo 校验状态转换的合法性，作为状态机守卫方法。
// 仅校验不修改状态；调用方校验通过后自行更新 Status 字段。
// 合法转换返回 nil，非法转换返回 error 描述当前状态与目标状态。
// 合法转换：Deducting(0) → Deducted(1) / Failed(5)；Deducted(1) → Credited(6) / Failed(5)。
func (r *RoundSettlement) TransitionTo(newStatus int) error {
	if r == nil {
		return errors.New("RoundSettlement is nil")
	}
	allowed := false
	switch r.Status {
	case RoundStatusDeducting:
		allowed = newStatus == RoundStatusDeducted || newStatus == RoundStatusFailed
	case RoundStatusDeducted:
		allowed = newStatus == RoundStatusCredited || newStatus == RoundStatusFailed
	}
	if !allowed {
		return fmt.Errorf("invalid round settlement status transition: %d -> %d", r.Status, newStatus)
	}
	return nil
}
