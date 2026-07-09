package application

import (
	"context"

	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/service"
)

// SettleAppService 是 settlement 模块的 Application 层入口，负责编排 settlement
// 各子 Service 的事务边界。外部调用方（如 game 模块的 GameEventHandler）应通过本
// facade 调用 settlement 用例，不直接依赖 settlement/service 下的具体 Service。
//
// 事务编排策略遵循：
//   - §13.1 #5：Repository 不开事务，由 AppService 通过 dbRepo.WithTransaction 编排。
//   - §5.4 短事务原则：禁止事务内 RPC，含 RPC 的用例由 Service 对 DB 写入片段开事务。
//
// 纯 DB 写入用例（SettleRound、DistributePenaltyFromPlatform、DeductForSystemPacket）
// 在此层开启事务并向下传递 tx；含 RPC 的用例由 Service 通过 dbRepo.WithTransaction
// 对 DB 写入片段开事务。
type SettleAppService struct {
	dbRepo                   repository.DBRepository
	roundSettleService       *service.RoundSettleService
	penaltySettlementService *service.PenaltySettlementService
	deductService            *service.DeductService
	balanceService           *service.BalanceService
}

// NewSettleAppService 构造 SettleAppService 实例。
// dbRepo 提供事务编排能力，各子 Service 由 bootstrap 创建并注入。
func NewSettleAppService(
	dbRepo repository.DBRepository,
	roundSettleService *service.RoundSettleService,
	penaltySettlementService *service.PenaltySettlementService,
	deductService *service.DeductService,
	balanceService *service.BalanceService,
) *SettleAppService {
	return &SettleAppService{
		dbRepo:                   dbRepo,
		roundSettleService:       roundSettleService,
		penaltySettlementService: penaltySettlementService,
		deductService:            deductService,
		balanceService:           balanceService,
	}
}

// SettleRound 编排单局结算用例：写 grab/commission BillRecord、触发奖励结算、
// 委托 GameSettleReportingService 完成会话级结算。
// 纯 DB 写入（creditRound + rewardSettler + 标记 Credited），在 AppService 层开启事务。
func (s *SettleAppService) SettleRound(ctx context.Context, req *dto.RoundSettleRequest) error {
	return s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		return s.roundSettleService.SettleRound(ctx, tx, req)
	})
}

// SettleGame 编排游戏级结算用例：对所有已 Credited 的回合执行会话级净额结算并上报平台。
// 含 platform.Settle / platform.Credit RPC，事务由 Service 内部对 DB 写入片段编排，
// AppService 仅转发（短事务原则：禁止事务内 RPC）。
func (s *SettleAppService) SettleGame(ctx context.Context, sessionID int64) error {
	return s.roundSettleService.SettleGame(ctx, sessionID)
}

// DeductPenaltyToPlatform 编排罚款扣款用例：从用户扣款上交平台。
// 纯转发到 penaltySettlementService.DeductPenaltyToPlatform，含 platform.Debit RPC，
// 事务由 Service 内部对 CreateBillsPair 片段编排（§5.4 短事务原则）。
func (s *SettleAppService) DeductPenaltyToPlatform(ctx context.Context, req *dto.PenaltyDeductRequest) error {
	return s.penaltySettlementService.DeductPenaltyToPlatform(ctx, req)
}

// DistributePenaltyFromPlatform 编排罚款分配用例：将平台罚款分配给指定接收方。
// 纯 DB 写入（批量 CreateBills），在 AppService 层开启事务。
func (s *SettleAppService) DistributePenaltyFromPlatform(ctx context.Context, req *dto.PenaltyDistributeRequest) error {
	return s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		return s.penaltySettlementService.DistributePenaltyFromPlatform(ctx, tx, req)
	})
}

// DeductForFirstRound 编排首回合扣款用例：创建 round_settlement + bills 并执行批量扣款。
// 含 platform.Debit RPC，事务由 Service 内部对 CreateRoundSettlementAndBills 片段编排。
func (s *SettleAppService) DeductForFirstRound(ctx context.Context, req *dto.FirstRoundDeductRequest) (*dto.FirstRoundDeductResult, error) {
	return s.deductService.DeductForFirstRound(ctx, req)
}

// DeductForLaterRound 编排后续回合扣款用例：最低金额玩家房费扣款。
// 含 platform.Debit RPC，事务由 Service 内部对 CreateRoundSettlementAndBills 片段编排。
func (s *SettleAppService) DeductForLaterRound(ctx context.Context, req *dto.LaterRoundDeductRequest) error {
	return s.deductService.DeductForLaterRound(ctx, req)
}

// DeductForSystemPacket 编排系统红包扣款用例：平台账户扣款。
// 纯 DB 写入（CreateRoundSettlementAndBills），在 AppService 层开启事务。
func (s *SettleAppService) DeductForSystemPacket(ctx context.Context, req *dto.SystemPacketDeductRequest) error {
	return s.dbRepo.WithTransaction(ctx, func(tx repository.Transaction) error {
		return s.deductService.DeductForSystemPacket(ctx, tx, req)
	})
}

// CheckBalance 查询用户余额是否满足所需金额。
// 纯转发到 balanceService.CheckBalance，只读用例，无需事务。
func (s *SettleAppService) CheckBalance(ctx context.Context, userID int64, requiredAmount int64) (int64, bool, error) {
	return s.balanceService.CheckBalance(ctx, userID, requiredAmount)
}

// CheckBalanceForReady 检查用户余额是否满足开局所需费用。
// 纯转发到 balanceService.CheckBalanceForReady，只读用例，无需事务。
func (s *SettleAppService) CheckBalanceForReady(ctx context.Context, req *dto.BalanceCheckRequest) (*dto.BalanceCheckResult, error) {
	return s.balanceService.CheckBalanceForReady(ctx, req)
}
