package domain

import (
	"context"
	"time"
)

type Grabber interface {
	GrabPacket(ctx context.Context, roomID, roundID, userID string) (*GrabResult, error)
}

type GrabResult struct {
	PacketID string `json:"packet_id"`
	Amount   int64  `json:"amount"`
	Position int    `json:"position"`
	IsLast   bool   `json:"is_last"`
}

type PacketInfo struct {
	PacketID  string    `json:"packet_id"`
	RoomID    string    `json:"room_id"`
	RoundID   string    `json:"round_id"`
	SenderID  string    `json:"sender_id"`
	Amount    int64     `json:"amount"`
	Position  int       `json:"position"`
	IsGrabbed bool      `json:"is_grabbed"`
	GrabberID string    `json:"grabber_id"`
	GrabbedAt time.Time `json:"grabbed_at"`
}

type PlayerResult struct {
	UserID        string `json:"user_id"`
	GrabbedAmount int64  `json:"grabbed_amount"`
	SentAmount    int64  `json:"sent_amount"`
	NetAmount     int64  `json:"net_amount"`
	Position      int    `json:"position"`
}

type SpecialReward struct {
	Type   string `json:"type"`
	UserID string `json:"user_id"`
	Amount int64  `json:"amount"`
	Detail string `json:"detail"`
}

type RoundRewardInfo struct {
	RewardType   int    `json:"reward_type"`
	RewardAmount int64  `json:"reward_amount"`
	TraceID      string `json:"trace_id"`
}

type DistributeResult struct {
	UserID   string `json:"user_id"`
	Amount   int64  `json:"amount"`
	Position int32  `json:"position"`
}
