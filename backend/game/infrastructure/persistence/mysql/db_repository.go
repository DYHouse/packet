package mysql

import (
	"context"

	"github.com/cashparty/backend/game/domain"
	"gorm.io/gorm"
)

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
	return &DBRepositoryImpl{db: db}
}

func (r *DBRepositoryImpl) RoomDBRepo() domain.RoomDBRepository {
	if r.roomRepo == nil {
		r.roomRepo = NewGormRoomRepository(r.db)
	}
	return r.roomRepo
}

func (r *DBRepositoryImpl) SessionDBRepo() domain.SessionDBRepository {
	if r.sessionRepo == nil {
		r.sessionRepo = NewGormSessionRepository(r.db)
	}
	return r.sessionRepo
}

func (r *DBRepositoryImpl) UserDBRepo() domain.UserDBRepository {
	if r.userRepo == nil {
		r.userRepo = NewGormUserRepository(r.db)
	}
	return r.userRepo
}

func (r *DBRepositoryImpl) RoomConfigDBRepo() domain.RoomConfigDBRepository {
	if r.roomConfigRepo == nil {
		r.roomConfigRepo = NewGormRoomConfigRepository(r.db)
	}
	return r.roomConfigRepo
}

func (r *DBRepositoryImpl) RoundDBRepo() domain.RoundDBRepository {
	if r.roundRepo == nil {
		r.roundRepo = NewGormRoundRepository(r.db)
	}
	return r.roundRepo
}

func (r *DBRepositoryImpl) HistoryDBRepo() domain.HistoryDBRepository {
	if r.historyRepo == nil {
		r.historyRepo = NewGormHistoryRepository(r.db)
	}
	return r.historyRepo
}

func (r *DBRepositoryImpl) WithTransaction(ctx context.Context, fn func(tx domain.Transaction) error) error {
	return r.db.WithContext(ctx).Transaction(func(gormTx *gorm.DB) error {
		tx := &GormTransactionImpl{db: gormTx}
		return fn(tx)
	})
}

type GormTransactionImpl struct {
	db          *gorm.DB
	roomRepo    domain.RoomDBRepository
	sessionRepo domain.SessionDBRepository
	userRepo    domain.UserDBRepository
	roundRepo   domain.RoundDBRepository
}

func (t *GormTransactionImpl) RoomDBRepo() domain.RoomDBRepository {
	if t.roomRepo == nil {
		t.roomRepo = NewGormRoomRepository(t.db)
	}
	return t.roomRepo
}

func (t *GormTransactionImpl) SessionDBRepo() domain.SessionDBRepository {
	if t.sessionRepo == nil {
		t.sessionRepo = NewGormSessionRepository(t.db)
	}
	return t.sessionRepo
}

func (t *GormTransactionImpl) UserDBRepo() domain.UserDBRepository {
	if t.userRepo == nil {
		t.userRepo = NewGormUserRepository(t.db)
	}
	return t.userRepo
}

func (t *GormTransactionImpl) RoundDBRepo() domain.RoundDBRepository {
	if t.roundRepo == nil {
		t.roundRepo = NewGormRoundRepository(t.db)
	}
	return t.roundRepo
}
