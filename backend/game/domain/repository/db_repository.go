package repository

import (
	"context"
	"time"

	"github.com/cashparty/backend/game/model"
)

type RoomDBRepository interface {
	GetRoom(ctx context.Context, roomID string) (*model.Room, error)
	UpdateRoom(ctx context.Context, roomID string, updates map[string]interface{}) error

	GetRoomList(ctx context.Context, configID, status, page, pageSize int) ([]*model.Room, error)
	GetRoomCount(ctx context.Context, configID, status int) (int64, error)
	MatchRoomByBalance(ctx context.Context, balance int64) (string, error)
}

type SessionDBRepository interface {
	GetSession(ctx context.Context, sessionID string) (*model.GameSession, error)
	GetSessionByID(ctx context.Context, sessionID int64) (*model.GameSession, error)
	CreateSession(ctx context.Context, session *model.GameSession) error
	UpdateSessionEnded(ctx context.Context, sessionID int64, actualRounds int, endedAt time.Time, endReason string) error
	UpdateSessionCurrentRound(ctx context.Context, sessionID int64, currentRound int) error
	CreateOrUpdateSessionPlayer(ctx context.Context, player *model.SessionPlayer) error
	IncrementSessionPlayerGrab(ctx context.Context, sessionID, userID int64, amount int64) error
	IncrementSessionPlayerSend(ctx context.Context, sessionID, userID int64, amount int64) error
}

type UserDBRepository interface {
	CreateOrUpdateUser(ctx context.Context, user *model.User) error
	GetUser(ctx context.Context, userID string) (*model.User, error)
	GetUserById(ctx context.Context, id string) (*model.User, error)
	SetUserIsRobot(ctx context.Context, id int64) error
}

type RoundDBRepository interface {
	CreateRound(ctx context.Context, round *model.Round) error
	// CreateOrGetRound 按 (session_id, round_no) 幂等创建 round。
	// 冲突时返回已存在的 round 记录（INSERT IGNORE + 查询模式，与 user_repository 一致）。
	CreateOrGetRound(ctx context.Context, round *model.Round) (*model.Round, error)
	UpdateRoundStatus(ctx context.Context, roundID int64, status model.RoundStatus) error
	UpdateRoundDeductInfo(ctx context.Context, roundID int64, deductScene, deductStatus int, deductAmount int64, batchID string) error
	UpdateRoundFailed(ctx context.Context, roundID int64, reason string) error
	UpdateRoundSender(ctx context.Context, roundID int64, senderID int64, senderType string) error
	UpdateRoundAmount(ctx context.Context, roundID int64, totalAmount, commission int64) error
	GetRoundByRoundID(ctx context.Context, roundID int64) (*model.Round, error)
	GetRoundBySessionAndRoundNo(ctx context.Context, sessionID int64, roundNo int) (*model.Round, error)
	UpdateRoundSending(ctx context.Context, roundID, senderID int64, senderType string, startedAt time.Time) error
	UpdateRoundEnded(ctx context.Context, roundID int64, settleTraceID int64, endedAt time.Time) error
}

// PacketDBRepository 红包数据库仓储接口
type PacketDBRepository interface {
	CreatePacket(ctx context.Context, packet *model.Packet) error
	CountPacketsByRoundID(ctx context.Context, roundID int64) (int64, error)
}

// GrabRecordRepository 抢包记录数据库仓储接口
type GrabRecordRepository interface {
	FirstOrCreateGrabRecord(ctx context.Context, record *model.RoundGrabRecord) error
}

// SpecialRewardRepository 特殊奖励数据库仓储接口
type SpecialRewardRepository interface {
	CreateSpecialReward(ctx context.Context, reward *model.SpecialReward) error
}

// PenaltyRecordRepository 罚款记录数据库仓储接口
type PenaltyRecordRepository interface {
	Create(ctx context.Context, record *model.PenaltyRecord) error
}

type Transaction interface {
	RoomDBRepo() RoomDBRepository
	SessionDBRepo() SessionDBRepository
	UserDBRepo() UserDBRepository
	RoundDBRepo() RoundDBRepository
	PacketDBRepo() PacketDBRepository
	GrabRecordRepo() GrabRecordRepository
	SpecialRewardRepo() SpecialRewardRepository
	PenaltyRecordRepo() PenaltyRecordRepository
}

// PlayerSessionRow 是 session_players + game_sessions 关联查询的行
type PlayerSessionRow struct {
	// session_players 字段
	SessionID int64
	Nickname  string
	Avatar    string
	SeatNo    int
	SendCount int
	GrabCount int
	TotalSend int64 // 分
	TotalGrab int64 // 分
	JoinedAt  time.Time
	// game_sessions 字段
	RoomNo       string
	ConfigName   string
	RoomFee      int64 // 分
	MaxRounds    int
	ActualRounds int
	Status       int
	StartedAt    *time.Time
	EndedAt      *time.Time
	EndReason    string
}

// PlayerStatsAggregate 玩家累计统计聚合
type PlayerStatsAggregate struct {
	TotalGames     int64
	WinCount       int64
	TotalSend      int64 // 分
	TotalGrab      int64 // 分
	TotalSendCount int64
	TotalGrabCount int64
}

// PlayerSessionBillRow 列表项（含 bill_record 聚合的盈亏数据）
type PlayerSessionBillRow struct {
	// game_sessions 字段
	SessionID    int64
	RoomNo       string
	ConfigName   string
	RoomFee      int64
	MaxRounds    int
	ActualRounds int
	Status       int
	StartedAt    *time.Time
	EndedAt      *time.Time
	EndReason    string
	// bill_record 聚合字段（分）
	TotalGrab     int64 // bill_type=3 求和
	TotalSend     int64 // bill_type=4 求和（ABS）
	FirstRoundFee int64 // bill_type=2 求和（ABS）
	Penalty       int64 // bill_type=8 求和（ABS）
	TotalBet      int64 // bill_type IN (2,4,8) 且 amount<0 求和（ABS）
	TotalIncome   int64 // bill_type IN (3,10,11) 且 amount>0 求和
	Reward        int64 // 系统奖励求和（bill_type IN (10,11)）
	Profit        int64 // TotalIncome - TotalBet
	GrabCount     int64 // bill_type=3 计数
	SendCount     int64 // bill_type=4 计数
	// session_players 字段
	SeatNo   int
	JoinedAt *time.Time
}

// PlayerSessionBillSummary 单局个人结果卡片（基于 bill_record 聚合）
type PlayerSessionBillSummary struct {
	TotalGrab     int64
	TotalSend     int64
	FirstRoundFee int64
	Penalty       int64
	TotalBet      int64
	TotalIncome   int64
	Reward        int64
	Profit        int64
	GrabCount     int64
	SendCount     int64
}

// PlayerStatsBillAggregate 玩家累计统计（基于 bill_record 聚合）
type PlayerStatsBillAggregate struct {
	TotalGames     int64 // 不同 session_id 计数
	WinCount       int64 // 盈亏>0 的 session 计数
	TotalGrab      int64
	TotalSend      int64
	FirstRoundFee  int64
	Penalty        int64
	TotalBet       int64
	TotalIncome    int64
	Reward         int64
	TotalProfit    int64
	TotalSendCount int64
	TotalGrabCount int64
}

// PlayerRoundPenaltyRow 玩家在某个 round 的罚款扣款明细（基于 bill_record 聚合）
type PlayerRoundPenaltyRow struct {
	RoundID     int64
	RoundNo     int
	Amount      int64  // ABS(SUM(amount))，正数表示支出
	PenaltyType string // bill_record.penalty_type
	Count       int    // 该 round 内被罚款次数
}

// PlayerRoundPenaltyDistRow 玩家在某个 round 收到的罚款分红明细（基于 bill_record 聚合）
type PlayerRoundPenaltyDistRow struct {
	RoundID int64
	RoundNo int
	Amount  int64 // SUM(amount)，正数表示收入
}

// HistoryDBRepository 玩家历史记录查询仓储
type HistoryDBRepository interface {
	GetSessionRounds(sessionID int64) ([]model.Round, error)
	ListPlayerGrabRecords(sessionID, userID int64) ([]model.RoundGrabRecord, error)
	GetPlayerSession(userID, sessionID int64) (*model.SessionPlayer, error)
	GetSession(sessionID int64) (*model.GameSession, error)
	// 基于 bill_record 聚合的对账查询
	ListPlayerSessionsWithBill(userID int64, startTime, endTime *time.Time, configName string, limit, offset int) ([]PlayerSessionBillRow, int64, error)
	GetPlayerSessionBillSummary(userID, sessionID int64) (*PlayerSessionBillSummary, error)
	AggregatePlayerStatsFromBill(userID int64) (*PlayerStatsBillAggregate, error)
	GetPlayerSendRounds(sessionID, userID int64) ([]model.Round, error)
	// ListSessionSpecialRewards 查询会话内所有特殊奖励记录（顺子/豹子，按 round_no 升序）
	ListSessionSpecialRewards(sessionID int64) ([]model.SpecialReward, error)
	// ListPlayerRoundPenalties 查询玩家在会话内每个 round 的罚款扣款明细（bill_type=8 且 amount<0）
	ListPlayerRoundPenalties(sessionID, userID int64) ([]PlayerRoundPenaltyRow, error)
	// ListPlayerRoundPenaltyDistributions 查询玩家在会话内每个 round 收到的罚款分红（bill_type=10 且 amount>0 且 user_id!=0）
	ListPlayerRoundPenaltyDistributions(sessionID, userID int64) ([]PlayerRoundPenaltyDistRow, error)
}

type DBRepository interface {
	RoomDBRepo() RoomDBRepository
	SessionDBRepo() SessionDBRepository
	UserDBRepo() UserDBRepository
	RoomConfigDBRepo() RoomConfigDBRepository
	RoundDBRepo() RoundDBRepository
	HistoryDBRepo() HistoryDBRepository
	PacketDBRepo() PacketDBRepository
	GrabRecordRepo() GrabRecordRepository
	SpecialRewardRepo() SpecialRewardRepository
	PenaltyRecordRepo() PenaltyRecordRepository
	WithTransaction(ctx context.Context, fn func(tx Transaction) error) error
}

type RoomConfigDBRepository interface {
	GetRoomTypeList(ctx context.Context) ([]*RoomTypeItem, error)
}

type RoomTypeItem struct {
	ID          int64
	Name        string
	RoomFee     int64
	MaxRounds   int
	TotalPeople int
}
