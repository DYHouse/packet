package round

import "time"

type PenaltyType int

const (
	PenaltyTypeSendTimeout PenaltyType = iota + 1
)

func (t PenaltyType) String() string {
	switch t {
	case PenaltyTypeSendTimeout:
		return "send_timeout"
	default:
		return "unknown"
	}
}

func ParsePenaltyType(s string) PenaltyType {
	switch s {
	case "send_timeout":
		return PenaltyTypeSendTimeout
	default:
		return PenaltyTypeSendTimeout
	}
}

type PenaltyRecord struct {
	ID           int64       `json:"id"`
	UserID       string      `json:"user_id"`
	RoomID       string      `json:"room_id"`
	RoundNo      int         `json:"round_no"`
	PenaltyType  PenaltyType `json:"penalty_type"`
	Amount       int64       `json:"amount"`
	Count        int         `json:"count"`
	KickRequired bool        `json:"kick_required"`
	CreatedAt    time.Time   `json:"created_at"`
}

type PenaltyResult struct {
	Applied      bool   `json:"applied"`
	Amount       int64  `json:"amount"`
	Count        int    `json:"count"`
	KickRequired bool   `json:"kick_required"`
	Reason       string `json:"reason"`
	DeductFailed bool   `json:"deduct_failed"`
	DeductError  error  `json:"-"`
}

type PenaltyPolicy struct {
	FirstPenaltyAmount  int64
	SecondPenaltyAmount int64
	KickOnSecond        bool
	MaxPenaltyCount     int
}

func DefaultPenaltyPolicy() *PenaltyPolicy {
	return &PenaltyPolicy{
		FirstPenaltyAmount:  0,
		SecondPenaltyAmount: 0,
		KickOnSecond:        true,
		MaxPenaltyCount:     2,
	}
}

func (p *PenaltyPolicy) ShouldKick(count int) bool {
	return count >= p.MaxPenaltyCount
}

type PenaltyDistribution struct {
	RoomID        string   `json:"room_id"`
	TotalAmount   int64    `json:"total_amount"`
	ShareAmount   int64    `json:"share_amount"`
	Recipients    []string `json:"recipients"`
	ExcludeUsers  []string `json:"exclude_users"`
	DistributedAt int64    `json:"distributed_at"`
}
