package push

import "github.com/cashparty/backend/common/currency"

// ==================== 游戏相关推送结构 ====================
//
// 本文件从 common/message/payload.go 迁移而来（P1-4 领域特定结构迁移）。
// 仅包含游戏推送 payload 结构，PingResponse 等网关协议结构已移至 gateway/protocol。

type GameStartPush struct {
	RoomID       string `json:"room_id"`
	CurrentRound int32  `json:"current_round"`
	MaxRounds    int32  `json:"max_rounds"`
}

type GameResumedPush struct {
	RoomID       string `json:"room_id"`
	CurrentRound int32  `json:"current_round"`
	NextSenderID string `json:"next_sender_id"`
	Message      string `json:"message"`
}

type CountdownStartPush struct {
	RoomID    string `json:"room_id"`
	Countdown int32  `json:"countdown"`
}

// RoundEndResult 回合结束推送中的单玩家结果（与 events.go 的 RoundResult 区分：
// 本结构用于 WS 推送，含 Nickname/Avatar/currency.Money；events.go 版本用于领域事件，含 int64 Amount）。
type RoundEndResult struct {
	UserID         string         `json:"user_id"`
	Nickname       string         `json:"nickname"`
	Avatar         string         `json:"avatar"`
	Amount         currency.Money `json:"amount"`
	Position       int32          `json:"position"`
	PacketID       string         `json:"packet_id"`
	IsAutoAssigned bool           `json:"is_auto_assigned"`
}

type GameResult struct {
	UserID      string         `json:"user_id"`
	Nickname    string         `json:"nickname"`
	Avatar      string         `json:"avatar"`
	TotalProfit currency.Money `json:"total_profit"`
	Rank        int32          `json:"rank"`
}

type KickedPush struct {
	RoomID  string `json:"room_id"`
	UserID  string `json:"user_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type PlayerReconnectedPush struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	SeatNo   int    `json:"seat_no"`
}

// ==================== 游戏相关推送结构 ====================

// RoundStartPush 回合开始推送
type RoundStartPush struct {
	RoomID         string           `json:"room_id"`
	RoundID        string           `json:"round_id"`
	CurrentRound   int32            `json:"current_round"`
	SenderID       string           `json:"sender_id"`
	SenderNickname string           `json:"sender_nickname"`
	SenderType     string           `json:"sender_type"`
	TotalAmount    currency.Money   `json:"total_amount"`
	Commission     currency.Money   `json:"commission"`
	ActualAmount   currency.Money   `json:"actual_amount"`
	PacketCount    int32            `json:"packet_count"`
	GrabTimeout    int32            `json:"grab_timeout"`
	Packets        []PacketInfoPush `json:"packets"`
}

// PacketInfoPush 红包信息（推送用，与 grab.go 的 PacketInfo 领域模型区分：
// 本结构仅含 PacketID/Position 用于 WS 推送；grab.go 版本含完整领域字段）。
type PacketInfoPush struct {
	PacketID string `json:"packet_id"`
	Position int32  `json:"position"`
}

// PacketGrabbedPush 红包被抢推送
type PacketGrabbedPush struct {
	RoomID   string         `json:"room_id"`
	RoundID  string         `json:"round_id"`
	PacketID string         `json:"packet_id"`
	Position int32          `json:"position"`
	UserID   string         `json:"user_id"`
	Nickname string         `json:"nickname"`
	Amount   currency.Money `json:"amount"`
	IsLast   bool           `json:"is_last"`
}

// RoundEndPush 回合结束推送
type RoundEndPush struct {
	RoomID          string           `json:"room_id"`
	RoundID         string           `json:"round_id"`
	CurrentRound    int32            `json:"current_round"`
	SenderID        string           `json:"sender_id"`
	TotalAmount     currency.Money   `json:"total_amount"`
	Commission      currency.Money   `json:"commission"`
	Results         []RoundEndResult `json:"results"`
	MinAmountPlayer string           `json:"min_amount_player"`
	NextSenderID    string           `json:"next_sender_id"`
	IsGameEnd       bool             `json:"is_game_end"`
	RewardType      int              `json:"reward_type,omitempty"`
	RewardAmount    currency.Money   `json:"reward_amount,omitempty"`
	FinalResults    []GameResult     `json:"final_results,omitempty"`
}

// AutoDistributePush 自动分配推送
type AutoDistributePush struct {
	RoomID           string                 `json:"room_id"`
	RoundID          string                 `json:"round_id"`
	DistributedCount int32                  `json:"distributed_count"`
	Results          []DistributeResultPush `json:"results"`
}

// DistributeResultPush 分配结果（推送用，与 grab.go 的 DistributeResult 领域模型区分：
// 本结构 Amount 为 currency.Money 用于 WS 推送；grab.go 版本 Amount 为 int64）。
type DistributeResultPush struct {
	UserID   string         `json:"user_id"`
	Amount   currency.Money `json:"amount"`
	Position int32          `json:"position"`
}

// PenaltyPush 惩罚推送
type PenaltyPush struct {
	RoomID        string         `json:"room_id"`
	UserID        string         `json:"user_id"`
	PenaltyType   string         `json:"penalty_type"`
	PenaltyAmount currency.Money `json:"penalty_amount"`
	PenaltyCount  int32          `json:"penalty_count"`
	KickRequired  bool           `json:"kick_required"`
	Reason        string         `json:"reason"`
}

// WaitReplacementPush 等待补位推送
type WaitReplacementPush struct {
	RoomID     string   `json:"room_id"`
	VacantSeat int32    `json:"vacant_seat"`
	LeftUserID string   `json:"left_user_id"`
	WaitTime   int32    `json:"wait_time"`
	Recipients []string `json:"recipients,omitempty"`
}

// SubstitutePush 自动替补上座推送
type SubstitutePush struct {
	RoomID    string      `json:"room_id"`
	UserID    string      `json:"user_id"`
	SeatNo    int32       `json:"seat_no"`
	RoomState interface{} `json:"room_state,omitempty"`
}

// GameInterruptedPush 游戏中断推送
type GameInterruptedPush struct {
	RoomID       string         `json:"room_id"`
	Reason       string         `json:"reason"`
	PenaltyShare currency.Money `json:"penalty_share"`
	Recipients   []string       `json:"recipients"`
}

// DequeuedPush 被移出排队队列推送（余额不足被自动移除等场景）
type DequeuedPush struct {
	RoomID  string `json:"room_id"`
	UserID  string `json:"user_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// UserProfileUpdatedPush 用户资料更新推送 payload。
// 推送时机：玩家通过 update_avatar 命令成功更新头像后。
// 推送目标：该用户的所有在线连接（跨节点 via Kafka）。
// 推送失败不影响主流程：客户端下次 auth_ok 仍会拿到最新 avatar。
type UserProfileUpdatedPush struct {
	Avatar   string `json:"avatar,omitempty"`
	Nickname string `json:"nickname,omitempty"`
}
