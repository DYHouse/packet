package mysql

import (
	"context"

	repository "github.com/cashparty/backend/game/domain/repository"
	"gorm.io/gorm"
)

// DBRepositoryImpl 聚合各子 repo。构造时一次性初始化所有子 repo，
// 方法直接返回字段，无 lazy init 竞态。NewGormXxxRepository 均为纯内存构造（仅 set db），
// eager init 零成本，且构造后字段只读，天然并发安全。
type DBRepositoryImpl struct {
	db                *gorm.DB
	roomRepo          repository.RoomDBRepository
	sessionRepo       repository.SessionDBRepository
	userRepo          repository.UserDBRepository
	roomConfigRepo    repository.RoomConfigDBRepository
	roundRepo         repository.RoundDBRepository
	historyRepo       repository.HistoryDBRepository
	packetRepo        repository.PacketDBRepository
	grabRecordRepo    repository.GrabRecordRepository
	specialRewardRepo repository.SpecialRewardRepository
	penaltyRecordRepo repository.PenaltyRecordRepository
	snapshotRepo      repository.SnapshotRepository
}

// NewDBRepository 创建 DB 聚合仓储。minRoomFee 为房间列表与自动匹配的最小房费（分），默认 500（5 元）。
func NewDBRepository(db *gorm.DB, minRoomFee int64) repository.DBRepository {
	return &DBRepositoryImpl{
		db:                db,
		roomRepo:          NewGormRoomRepository(db, minRoomFee),
		sessionRepo:       NewGormSessionRepository(db),
		userRepo:          NewGormUserRepository(db),
		roomConfigRepo:    NewGormRoomConfigRepository(db, minRoomFee),
		roundRepo:         NewGormRoundRepository(db),
		historyRepo:       NewGormHistoryRepository(db),
		packetRepo:        NewGormPacketRepository(db),
		grabRecordRepo:    NewGormGrabRecordRepository(db),
		specialRewardRepo: NewGormSpecialRewardRepository(db),
		penaltyRecordRepo: NewGormPenaltyRecordRepository(db),
		snapshotRepo:      NewGormSnapshotRepository(db),
	}
}

func (r *DBRepositoryImpl) RoomDBRepo() repository.RoomDBRepository       { return r.roomRepo }
func (r *DBRepositoryImpl) SessionDBRepo() repository.SessionDBRepository { return r.sessionRepo }
func (r *DBRepositoryImpl) UserDBRepo() repository.UserDBRepository       { return r.userRepo }
func (r *DBRepositoryImpl) RoomConfigDBRepo() repository.RoomConfigDBRepository {
	return r.roomConfigRepo
}
func (r *DBRepositoryImpl) RoundDBRepo() repository.RoundDBRepository       { return r.roundRepo }
func (r *DBRepositoryImpl) HistoryDBRepo() repository.HistoryDBRepository   { return r.historyRepo }
func (r *DBRepositoryImpl) PacketDBRepo() repository.PacketDBRepository     { return r.packetRepo }
func (r *DBRepositoryImpl) GrabRecordRepo() repository.GrabRecordRepository { return r.grabRecordRepo }
func (r *DBRepositoryImpl) SpecialRewardRepo() repository.SpecialRewardRepository {
	return r.specialRewardRepo
}
func (r *DBRepositoryImpl) PenaltyRecordRepo() repository.PenaltyRecordRepository {
	return r.penaltyRecordRepo
}
func (r *DBRepositoryImpl) SnapshotDBRepo() repository.SnapshotRepository { return r.snapshotRepo }

func (r *DBRepositoryImpl) WithTransaction(ctx context.Context, fn func(tx repository.Transaction) error) error {
	return r.db.WithContext(ctx).Transaction(func(gormTx *gorm.DB) error {
		tx := NewGormTransaction(gormTx)
		return fn(tx)
	})
}

// GormTransactionImpl 事务内的子 repo 聚合。构造时一次性初始化，
// 避免事务内 lazy init 竞态（虽然事务通常单 goroutine 使用，但保持与 DBRepositoryImpl 一致）。
type GormTransactionImpl struct {
	db                *gorm.DB
	roomRepo          repository.RoomDBRepository
	sessionRepo       repository.SessionDBRepository
	userRepo          repository.UserDBRepository
	roundRepo         repository.RoundDBRepository
	packetRepo        repository.PacketDBRepository
	grabRecordRepo    repository.GrabRecordRepository
	specialRewardRepo repository.SpecialRewardRepository
	penaltyRecordRepo repository.PenaltyRecordRepository
	snapshotRepo      repository.SnapshotRepository
}

func NewGormTransaction(db *gorm.DB) *GormTransactionImpl {
	// 事务内 roomRepo 仅用于写操作（如 UpdateRoom），不执行房间列表查询，
	// 因此不注入 minRoomFee 过滤（传 0），避免事务构造签名随列表过滤策略变化。
	return &GormTransactionImpl{
		db:                db,
		roomRepo:          NewGormRoomRepository(db, 0),
		sessionRepo:       NewGormSessionRepository(db),
		userRepo:          NewGormUserRepository(db),
		roundRepo:         NewGormRoundRepository(db),
		packetRepo:        NewGormPacketRepository(db),
		grabRecordRepo:    NewGormGrabRecordRepository(db),
		specialRewardRepo: NewGormSpecialRewardRepository(db),
		penaltyRecordRepo: NewGormPenaltyRecordRepository(db),
		snapshotRepo:      NewGormSnapshotRepository(db),
	}
}

func (t *GormTransactionImpl) RoomDBRepo() repository.RoomDBRepository       { return t.roomRepo }
func (t *GormTransactionImpl) SessionDBRepo() repository.SessionDBRepository { return t.sessionRepo }
func (t *GormTransactionImpl) UserDBRepo() repository.UserDBRepository       { return t.userRepo }
func (t *GormTransactionImpl) RoundDBRepo() repository.RoundDBRepository     { return t.roundRepo }
func (t *GormTransactionImpl) PacketDBRepo() repository.PacketDBRepository   { return t.packetRepo }
func (t *GormTransactionImpl) GrabRecordRepo() repository.GrabRecordRepository {
	return t.grabRecordRepo
}
func (t *GormTransactionImpl) SpecialRewardRepo() repository.SpecialRewardRepository {
	return t.specialRewardRepo
}
func (t *GormTransactionImpl) PenaltyRecordRepo() repository.PenaltyRecordRepository {
	return t.penaltyRecordRepo
}
func (t *GormTransactionImpl) SnapshotRepo() repository.SnapshotRepository { return t.snapshotRepo }
