package mysql

import (
	"context"

	"github.com/cashparty/backend/game/domain"
	"gorm.io/gorm"
)

// DBRepositoryImpl 聚合各子 repo。构造时一次性初始化所有子 repo，
// 方法直接返回字段，无 lazy init 竞态。NewGormXxxRepository 均为纯内存构造（仅 set db），
// eager init 零成本，且构造后字段只读，天然并发安全。
type DBRepositoryImpl struct {
	db             *gorm.DB
	roomRepo       domain.RoomDBRepository
	sessionRepo    domain.SessionDBRepository
	userRepo       domain.UserDBRepository
	roomConfigRepo domain.RoomConfigDBRepository
	roundRepo      domain.RoundDBRepository
	historyRepo    domain.HistoryDBRepository
}

func NewDBRepository(db *gorm.DB) domain.DBRepository {
	return &DBRepositoryImpl{
		db:             db,
		roomRepo:       NewGormRoomRepository(db),
		sessionRepo:    NewGormSessionRepository(db),
		userRepo:       NewGormUserRepository(db),
		roomConfigRepo: NewGormRoomConfigRepository(db),
		roundRepo:      NewGormRoundRepository(db),
		historyRepo:    NewGormHistoryRepository(db),
	}
}

func (r *DBRepositoryImpl) RoomDBRepo() domain.RoomDBRepository             { return r.roomRepo }
func (r *DBRepositoryImpl) SessionDBRepo() domain.SessionDBRepository       { return r.sessionRepo }
func (r *DBRepositoryImpl) UserDBRepo() domain.UserDBRepository             { return r.userRepo }
func (r *DBRepositoryImpl) RoomConfigDBRepo() domain.RoomConfigDBRepository { return r.roomConfigRepo }
func (r *DBRepositoryImpl) RoundDBRepo() domain.RoundDBRepository           { return r.roundRepo }
func (r *DBRepositoryImpl) HistoryDBRepo() domain.HistoryDBRepository       { return r.historyRepo }

// DB 返回底层 *gorm.DB，供尚未抽象为 repo 方法的非事务读操作使用（如幂等检查）。
// 过渡期保留：后续 Phase 将逐步补齐 repo 方法并移除此方法。
func (r *DBRepositoryImpl) DB() *gorm.DB { return r.db }

func (r *DBRepositoryImpl) WithTransaction(ctx context.Context, fn func(tx domain.Transaction) error) error {
	return r.db.WithContext(ctx).Transaction(func(gormTx *gorm.DB) error {
		tx := NewGormTransaction(gormTx)
		return fn(tx)
	})
}

// GormTransactionImpl 事务内的子 repo 聚合。构造时一次性初始化，
// 避免事务内 lazy init 竞态（虽然事务通常单 goroutine 使用，但保持与 DBRepositoryImpl 一致）。
type GormTransactionImpl struct {
	db          *gorm.DB
	roomRepo    domain.RoomDBRepository
	sessionRepo domain.SessionDBRepository
	userRepo    domain.UserDBRepository
	roundRepo   domain.RoundDBRepository
}

func NewGormTransaction(db *gorm.DB) *GormTransactionImpl {
	return &GormTransactionImpl{
		db:          db,
		roomRepo:    NewGormRoomRepository(db),
		sessionRepo: NewGormSessionRepository(db),
		userRepo:    NewGormUserRepository(db),
		roundRepo:   NewGormRoundRepository(db),
	}
}

func (t *GormTransactionImpl) RoomDBRepo() domain.RoomDBRepository       { return t.roomRepo }
func (t *GormTransactionImpl) SessionDBRepo() domain.SessionDBRepository { return t.sessionRepo }
func (t *GormTransactionImpl) UserDBRepo() domain.UserDBRepository       { return t.userRepo }
func (t *GormTransactionImpl) RoundDBRepo() domain.RoundDBRepository     { return t.roundRepo }

// DB 返回事务内的底层 *gorm.DB，供尚未抽象为 repo 方法的原始写操作使用。
// 过渡期保留：后续 Phase 将逐步补齐 repo 方法并移除此方法。
func (t *GormTransactionImpl) DB() *gorm.DB { return t.db }
