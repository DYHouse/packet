package mysql

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/domain/repository"
	"gorm.io/gorm"
)

// dbRepositoryImpl 聚合 settlement 各子 repo。构造时一次性初始化所有子 repo，
// 方法直接返回字段，无 lazy init 竞态。NewXxxRepository 均为纯内存构造（仅 set db），
// eager init 零成本，且构造后字段只读，天然并发安全。
// 参照 game/infrastructure/persistence/mysql/db_repository.go 模式。
type dbRepositoryImpl struct {
	db                      *gorm.DB
	transactionTimeout      time.Duration
	billRepo                repository.BillRepository
	roundSettlementRepo     repository.RoundSettlementRepository
	refundAuditRepo         repository.RefundAuditRepository
	settlementQueryRepo     repository.SettlementQueryRepository
	exceptionRepo           repository.ExceptionRepository
	platformCallLogRepo     repository.PlatformCallLogRepository
	penaltyDistributionRepo repository.PenaltyDistributionRepository
}

// NewDBRepository 创建 settlement DBRepository 实例，聚合所有子 repo。
// transactionTimeout 控制 WithTransaction 的 ctx 超时，传入 0 时使用 30s 兜底（与原硬编码一致）。
func NewDBRepository(db *gorm.DB, transactionTimeout time.Duration) repository.DBRepository {
	if transactionTimeout <= 0 {
		transactionTimeout = 30 * time.Second
	}
	return &dbRepositoryImpl{
		db:                      db,
		transactionTimeout:      transactionTimeout,
		billRepo:                NewBillRepository(db),
		roundSettlementRepo:     NewRoundSettlementRepository(db),
		refundAuditRepo:         NewRefundAuditRepository(db),
		settlementQueryRepo:     NewSettlementQueryRepository(db),
		exceptionRepo:           NewExceptionRepository(db),
		platformCallLogRepo:     NewPlatformCallLogRepository(db),
		penaltyDistributionRepo: NewGormPenaltyDistributionRepository(db),
	}
}

func (r *dbRepositoryImpl) BillRepo() repository.BillRepository { return r.billRepo }
func (r *dbRepositoryImpl) RoundSettlementRepo() repository.RoundSettlementRepository {
	return r.roundSettlementRepo
}
func (r *dbRepositoryImpl) RefundAuditRepo() repository.RefundAuditRepository {
	return r.refundAuditRepo
}
func (r *dbRepositoryImpl) SettlementQueryRepo() repository.SettlementQueryRepository {
	return r.settlementQueryRepo
}

// ExceptionRepo / PlatformCallLogRepo 返回构造时 eager 初始化的子 repo 实例。
func (r *dbRepositoryImpl) ExceptionRepo() repository.ExceptionRepository {
	return r.exceptionRepo
}
func (r *dbRepositoryImpl) PlatformCallLogRepo() repository.PlatformCallLogRepository {
	return r.platformCallLogRepo
}
func (r *dbRepositoryImpl) PenaltyDistributionRepo() repository.PenaltyDistributionRepository {
	return r.penaltyDistributionRepo
}

// WithTransaction 编排事务。通过 ctx 超时控制（由 transactionTimeout 注入，默认 30s）遵循短事务原则，
// 事务回调内通过 tx 子 repo 访问器获取基于事务连接的子 repo。
func (r *dbRepositoryImpl) WithTransaction(ctx context.Context, fn func(tx repository.Transaction) error) error {
	ctx, cancel := context.WithTimeout(ctx, r.transactionTimeout)
	defer cancel()
	return r.db.WithContext(ctx).Transaction(func(gormTx *gorm.DB) error {
		tx := newGormTransaction(gormTx)
		return fn(tx)
	})
}

// gormTransactionImpl 事务内的子 repo 聚合。构造时一次性初始化，
// 避免事务内 lazy init 竞态（保持与 dbRepositoryImpl 一致）。
type gormTransactionImpl struct {
	tx                      *gorm.DB
	billRepo                repository.BillRepository
	roundSettlementRepo     repository.RoundSettlementRepository
	refundAuditRepo         repository.RefundAuditRepository
	settlementQueryRepo     repository.SettlementQueryRepository
	exceptionRepo           repository.ExceptionRepository
	platformCallLogRepo     repository.PlatformCallLogRepository
	penaltyDistributionRepo repository.PenaltyDistributionRepository
}

func newGormTransaction(db *gorm.DB) *gormTransactionImpl {
	return &gormTransactionImpl{
		tx:                      db,
		billRepo:                NewBillRepository(db),
		roundSettlementRepo:     NewRoundSettlementRepository(db),
		refundAuditRepo:         NewRefundAuditRepository(db),
		settlementQueryRepo:     NewSettlementQueryRepository(db),
		exceptionRepo:           NewExceptionRepository(db),
		platformCallLogRepo:     NewPlatformCallLogRepository(db),
		penaltyDistributionRepo: NewGormPenaltyDistributionRepository(db),
	}
}

func (t *gormTransactionImpl) BillRepo() repository.BillRepository { return t.billRepo }
func (t *gormTransactionImpl) RoundSettlementRepo() repository.RoundSettlementRepository {
	return t.roundSettlementRepo
}
func (t *gormTransactionImpl) RefundAuditRepo() repository.RefundAuditRepository {
	return t.refundAuditRepo
}
func (t *gormTransactionImpl) SettlementQueryRepo() repository.SettlementQueryRepository {
	return t.settlementQueryRepo
}

// ExceptionRepo / PlatformCallLogRepo 返回基于事务连接构造的子 repo 实例，确保事务内操作。
func (t *gormTransactionImpl) ExceptionRepo() repository.ExceptionRepository {
	return t.exceptionRepo
}
func (t *gormTransactionImpl) PlatformCallLogRepo() repository.PlatformCallLogRepository {
	return t.platformCallLogRepo
}
func (t *gormTransactionImpl) PenaltyDistributionRepo() repository.PenaltyDistributionRepository {
	return t.penaltyDistributionRepo
}
