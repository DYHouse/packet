package mysql

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/domain"
	"gorm.io/gorm"
)

// dbRepositoryImpl 聚合 settlement 各子 repo。构造时一次性初始化所有子 repo，
// 方法直接返回字段，无 lazy init 竞态。NewXxxRepository 均为纯内存构造（仅 set db），
// eager init 零成本，且构造后字段只读，天然并发安全。
// 参照 game/infrastructure/persistence/mysql/db_repository.go 模式。
type dbRepositoryImpl struct {
	db                  *gorm.DB
	billRepo            domain.BillRepository
	roundSettlementRepo domain.RoundSettlementRepository
	refundAuditRepo     domain.RefundAuditRepository
	settlementQueryRepo domain.SettlementQueryRepository
}

// NewDBRepository 创建 settlement DBRepository 实例，聚合所有子 repo。
func NewDBRepository(db *gorm.DB) domain.DBRepository {
	return &dbRepositoryImpl{
		db:                  db,
		billRepo:            NewBillRepository(db),
		roundSettlementRepo: NewRoundSettlementRepository(db),
		refundAuditRepo:     NewRefundAuditRepository(db),
		settlementQueryRepo: NewSettlementQueryRepository(db),
	}
}

func (r *dbRepositoryImpl) BillRepo() domain.BillRepository { return r.billRepo }
func (r *dbRepositoryImpl) RoundSettlementRepo() domain.RoundSettlementRepository {
	return r.roundSettlementRepo
}
func (r *dbRepositoryImpl) RefundAuditRepo() domain.RefundAuditRepository {
	return r.refundAuditRepo
}
func (r *dbRepositoryImpl) SettlementQueryRepo() domain.SettlementQueryRepository {
	return r.settlementQueryRepo
}

// WithTransaction 编排事务。通过 ctx 超时控制（默认 30s）遵循短事务原则，
// 事务回调内通过 tx 子 repo 访问器获取基于事务连接的子 repo。
func (r *dbRepositoryImpl) WithTransaction(ctx context.Context, fn func(tx domain.Transaction) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return r.db.WithContext(ctx).Transaction(func(gormTx *gorm.DB) error {
		tx := newGormTransaction(gormTx)
		return fn(tx)
	})
}

// gormTransactionImpl 事务内的子 repo 聚合。构造时一次性初始化，
// 避免事务内 lazy init 竞态（保持与 dbRepositoryImpl 一致）。
type gormTransactionImpl struct {
	tx                  *gorm.DB
	billRepo            domain.BillRepository
	roundSettlementRepo domain.RoundSettlementRepository
	refundAuditRepo     domain.RefundAuditRepository
	settlementQueryRepo domain.SettlementQueryRepository
}

func newGormTransaction(db *gorm.DB) *gormTransactionImpl {
	return &gormTransactionImpl{
		tx:                  db,
		billRepo:            NewBillRepository(db),
		roundSettlementRepo: NewRoundSettlementRepository(db),
		refundAuditRepo:     NewRefundAuditRepository(db),
		settlementQueryRepo: NewSettlementQueryRepository(db),
	}
}

func (t *gormTransactionImpl) BillRepo() domain.BillRepository { return t.billRepo }
func (t *gormTransactionImpl) RoundSettlementRepo() domain.RoundSettlementRepository {
	return t.roundSettlementRepo
}
func (t *gormTransactionImpl) RefundAuditRepo() domain.RefundAuditRepository {
	return t.refundAuditRepo
}
func (t *gormTransactionImpl) SettlementQueryRepo() domain.SettlementQueryRepository {
	return t.settlementQueryRepo
}
