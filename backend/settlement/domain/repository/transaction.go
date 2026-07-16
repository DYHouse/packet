package repository

import "context"

// Transaction 事务接口，提供事务内的子 repo 访问器。
// 参照 game/domain/db_repository.go 模式：Repository 不开事务，由 AppService
// 通过 DBRepository.WithTransaction 编排（见 GAME_SERVICE_ARCHITECTURE_REVIEW.md §13.1 #5）。
type Transaction interface {
	BillRepo() BillRepository
	RoundSettlementRepo() RoundSettlementRepository
	RefundAuditRepo() RefundAuditRepository
	SettlementQueryRepo() SettlementQueryRepository
	ExceptionRepo() ExceptionRepository
	PlatformCallLogRepo() PlatformCallLogRepository
	PenaltyDistributionRepo() PenaltyDistributionRepository
}

// DBRepository 数据库仓储接口，提供事务编排能力。
// AppService 通过 WithTransaction 开启事务，在回调内通过 tx 子 repo 访问器
// 获取基于事务连接的子 repo，实现跨表原子性，Repository 自身不再开事务。
type DBRepository interface {
	BillRepo() BillRepository
	RoundSettlementRepo() RoundSettlementRepository
	RefundAuditRepo() RefundAuditRepository
	SettlementQueryRepo() SettlementQueryRepository
	ExceptionRepo() ExceptionRepository
	PlatformCallLogRepo() PlatformCallLogRepository
	PenaltyDistributionRepo() PenaltyDistributionRepository
	WithTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
