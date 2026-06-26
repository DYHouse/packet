package domain

import (
	"context"
	"time"

	"github.com/cashparty/backend/game/model"
)

type RoomDBRepository interface {
	CreateRoom(ctx context.Context, room *model.Room) error
	GetRoom(ctx context.Context, roomID string) (*model.Room, error)
	GetRoomByRoomNo(ctx context.Context, roomNo string) (*model.Room, error)
	UpdateRoom(ctx context.Context, roomID string, updates map[string]interface{}) error
	UpdateRoomStatus(ctx context.Context, roomID string, status model.RoomStatus, startedAt *int64) error
	DeleteRoom(ctx context.Context, roomID string) error

	GetRoomList(ctx context.Context, configID, status, page, pageSize int) ([]*model.Room, error)
	GetRoomCount(ctx context.Context, configID, status int) (int64, error)
	GetAllRooms(ctx context.Context) ([]*model.Room, error)
	MatchRoomByBalance(ctx context.Context, balance int64) (string, error)
}

type SessionDBRepository interface {
	CreateSession(ctx context.Context, session *model.GameSession) error
	GetSession(ctx context.Context, sessionID string) (*model.GameSession, error)
	GetActiveSessionByRoom(ctx context.Context, roomID string) (*model.GameSession, error)
	UpdateSession(ctx context.Context, sessionID string, updates map[string]interface{}) error
	EndSession(ctx context.Context, sessionID string, actualRounds int, reason string) error

	CreateSessionPlayer(ctx context.Context, player *model.SessionPlayer) error
	GetSessionPlayers(ctx context.Context, sessionID string) ([]*model.SessionPlayer, error)
	UpdateSessionPlayer(ctx context.Context, sessionID string, userID string, updates map[string]interface{}) error
	BatchUpdateSessionPlayerStats(ctx context.Context, sessionID string, playerStats map[string]*PlayerStatsUpdate) error
}

type UserDBRepository interface {
	CreateOrUpdateUser(ctx context.Context, user *model.User) error
	GetUser(ctx context.Context, userID string) (*model.User, error)
	GetUserById(ctx context.Context, id string) (*model.User, error)
	SetUserIsRobot(ctx context.Context, id int64) error
}

type RoundDBRepository interface {
	CreateRound(ctx context.Context, round *model.Round) error
	GetRound(ctx context.Context, roundID int64) (*model.Round, error)
	GetRoundBySessionAndNo(ctx context.Context, sessionID int64, roundNo int) (*model.Round, error)
	UpdateRoundStatus(ctx context.Context, roundID int64, status model.RoundStatus) error
	UpdateRoundDeductInfo(ctx context.Context, roundID int64, deductScene, deductStatus int, deductAmount int64, batchID string) error
	UpdateRoundFailed(ctx context.Context, roundID int64, reason string) error
	UpdateRoundSender(ctx context.Context, roundID int64, senderID int64, senderType string) error
	UpdateRoundAmount(ctx context.Context, roundID int64, totalAmount, commission int64) error
}

type Transaction interface {
	RoomDBRepo() RoomDBRepository
	SessionDBRepo() SessionDBRepository
	UserDBRepo() UserDBRepository
	RoundDBRepo() RoundDBRepository
}

type PlayerStatsUpdate struct {
	TotalSend   int64
	TotalGrab   int64
	TotalProfit int64
	SendCount   int
	GrabCount   int
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
	LeftAt    *time.Time
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
	TotalIncome int64 // bill_type IN (3,10,11) 且 amount>0 求和
	Profit      int64 // TotalIncome - TotalBet
	GrabCount   int64 // bill_type=3 计数
	SendCount   int64 // bill_type=4 计数
}

// PlayerSessionBillSummary 单局个人结果卡片（基于 bill_record 聚合）
type PlayerSessionBillSummary struct {
	TotalGrab     int64
	TotalSend     int64
	FirstRoundFee int64
	Penalty       int64
	TotalBet      int64
	TotalIncome int64
	Profit      int64
	GrabCount   int64
	SendCount   int64
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
	TotalProfit    int64
	TotalSendCount int64
	TotalGrabCount int64
}

// HistoryDBRepository 玩家历史记录查询仓储
type HistoryDBRepository interface {
	ListPlayerSessions(userID int64, startTime, endTime *time.Time, configName string, limit, offset int) ([]PlayerSessionRow, int64, error)
	GetSessionRounds(sessionID int64) ([]model.Round, error)
	ListPlayerGrabRecords(sessionID, userID int64) ([]model.RoundGrabRecord, error)
	AggregatePlayerStats(userID int64) (*PlayerStatsAggregate, error)
	GetPlayerSession(userID, sessionID int64) (*model.SessionPlayer, error)
	GetSession(sessionID int64) (*model.GameSession, error)
	// 基于 bill_record 聚合的对账查询
	ListPlayerSessionsWithBill(userID int64, startTime, endTime *time.Time, configName string, limit, offset int) ([]PlayerSessionBillRow, int64, error)
	GetPlayerSessionBillSummary(userID, sessionID int64) (*PlayerSessionBillSummary, error)
	AggregatePlayerStatsFromBill(userID int64) (*PlayerStatsBillAggregate, error)
	GetPlayerSendRounds(sessionID, userID int64) ([]model.Round, error)
}

type DBRepository interface {
	RoomDBRepo() RoomDBRepository
	SessionDBRepo() SessionDBRepository
	UserDBRepo() UserDBRepository
	RoomConfigDBRepo() RoomConfigDBRepository
	RoundDBRepo() RoundDBRepository
	HistoryDBRepo() HistoryDBRepository
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
