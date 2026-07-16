package application

import (
	"time"

	"github.com/cashparty/backend/common/currency"
)

// PlayerHistoryReq 玩家历史对局列表请求
type PlayerHistoryReq struct {
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
	StartDate  *time.Time `json:"start_date,omitempty"`
	EndDate    *time.Time `json:"end_date,omitempty"`
	ConfigName string     `json:"config_name,omitempty"`
}

// PlayerHistoryItem 玩家历史对局列表项（含会话信息 + 玩家个人数据）
type PlayerHistoryItem struct {
	SessionID     string         `json:"session_id"`
	RoomNo        string         `json:"room_no"`
	ConfigName    string         `json:"config_name"`
	RoomFee       currency.Money `json:"room_fee"`
	MaxRounds     int            `json:"max_rounds"`
	ActualRounds  int            `json:"actual_rounds"`
	Status        int            `json:"status"`
	StartedAt     int64          `json:"started_at"`
	EndedAt       int64          `json:"ended_at"`
	EndReason     string         `json:"end_reason"`
	SeatNo        int            `json:"seat_no"`
	SendCount     int            `json:"send_count"`
	GrabCount     int            `json:"grab_count"`
	TotalSend     currency.Money `json:"total_send"`
	FirstRoundFee currency.Money `json:"first_round_fee"`
	Penalty       currency.Money `json:"penalty"`
	TotalBet      currency.Money `json:"total_bet"`
	TotalGrab     currency.Money `json:"total_grab"`
	Reward        currency.Money `json:"reward"`       // 系统奖励（罚款分发+特殊奖励）
	TotalIncome   currency.Money `json:"total_income"` // 总收入（抢包+系统奖励）
	Profit        currency.Money `json:"profit"`
	JoinedAt      int64          `json:"joined_at"`
}

// PlayerHistoryResp 玩家历史对局列表响应
type PlayerHistoryResp struct {
	List     []PlayerHistoryItem `json:"list"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

// SessionInfo 会话基本信息
type SessionInfo struct {
	SessionID    string         `json:"session_id"`
	RoomNo       string         `json:"room_no"`
	ConfigName   string         `json:"config_name"`
	RoomFee      currency.Money `json:"room_fee"`
	MaxRounds    int            `json:"max_rounds"`
	ActualRounds int            `json:"actual_rounds"`
	Status       int            `json:"status"`
	StartedAt    int64          `json:"started_at"`
	EndedAt      int64          `json:"ended_at"`
	EndReason    string         `json:"end_reason"`
}

// GrabDetail 抢包明细
type GrabDetail struct {
	PacketID       string         `json:"packet_id"`
	Amount         currency.Money `json:"amount"`
	IsMin          bool           `json:"is_min"`
	IsAutoAssigned bool           `json:"is_auto_assigned"`
	GrabbedAt      int64          `json:"grabbed_at"`
}

// SendDetail 发包明细（玩家作为发包者时的发出金额）
type SendDetail struct {
	TotalAmount currency.Money `json:"total_amount"`
	StartedAt   int64          `json:"started_at"`
}

// RewardDetail 特殊奖励明细（顺子/豹子）
type RewardDetail struct {
	RewardType  int            `json:"reward_type"`  // 1顺子 2豹子
	Amount      currency.Money `json:"amount"`       // 每玩家奖励金额
	TriggerType int            `json:"trigger_type"` // 1保底 2概率
}

// PenaltyDetail 罚款明细（玩家被罚款，基于 bill_record bill_type=8 聚合）
type PenaltyDetail struct {
	PenaltyType string         `json:"penalty_type"` // 罚款类型，如 SendTimeout/ReplaceTimeout
	Amount      currency.Money `json:"amount"`       // 罚款金额（正数，表示支出）
	Count       int            `json:"count"`        // 罚款次数
}

// PenaltyDistributionDetail 罚款分红明细（玩家收到罚款分红，基于 bill_record bill_type=10 聚合）
type PenaltyDistributionDetail struct {
	Amount currency.Money `json:"amount"` // 分红金额（正数，表示收入）
}

// RoundDetail 回合明细
type RoundDetail struct {
	RoundID               string                     `json:"round_id"`
	RoundNo               int                        `json:"round_no"`
	SenderID              string                     `json:"sender_id"`
	SenderType            string                     `json:"sender_type"`
	TotalAmount           currency.Money             `json:"total_amount"`
	StartedAt             int64                      `json:"started_at"`
	EndedAt               int64                      `json:"ended_at"`
	Status                int                        `json:"status"`
	MyGrab                *GrabDetail                `json:"my_grab,omitempty"`
	MySend                *SendDetail                `json:"my_send,omitempty"`
	MyReward              *RewardDetail              `json:"my_reward,omitempty"`
	MyPenalty             *PenaltyDetail             `json:"my_penalty,omitempty"`
	MyPenaltyDistribution *PenaltyDistributionDetail `json:"my_penalty_distribution,omitempty"`
}

// PlayerSessionDetailResp 单局详情响应
type PlayerSessionDetailResp struct {
	Session SessionInfo       `json:"session"`
	MyStats PlayerHistoryItem `json:"my_stats"`
	Rounds  []RoundDetail     `json:"rounds"`
}

// PlayerStatsResp 玩家累计统计响应
type PlayerStatsResp struct {
	TotalGames     int64          `json:"total_games"`
	WinCount       int64          `json:"win_count"`
	LoseCount      int64          `json:"lose_count"`
	WinRate        float64        `json:"win_rate"`
	TotalProfit    currency.Money `json:"total_profit"`
	TotalSend      currency.Money `json:"total_send"`
	FirstRoundFee  currency.Money `json:"first_round_fee"`
	Penalty        currency.Money `json:"penalty"`
	TotalBet       currency.Money `json:"total_bet"`
	TotalGrab      currency.Money `json:"total_grab"`
	Reward         currency.Money `json:"reward"`       // 系统奖励（罚款分发+特殊奖励）
	TotalIncome    currency.Money `json:"total_income"` // 总收入（抢包+系统奖励）
	TotalSendCount int64          `json:"total_send_count"`
	TotalGrabCount int64          `json:"total_grab_count"`
	AvgProfit      currency.Money `json:"avg_profit"`
}
