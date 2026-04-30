package domain

import (
	"context"

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

type DBRepository interface {
	RoomDBRepo() RoomDBRepository
	SessionDBRepo() SessionDBRepository
	UserDBRepo() UserDBRepository
	RoomConfigDBRepo() RoomConfigDBRepository
	RoundDBRepo() RoundDBRepository
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
