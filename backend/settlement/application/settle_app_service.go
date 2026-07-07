package application

import (
	"context"

	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/service"
)

// SettleAppService 是 settlement 模块的 Application 层入口，作为编排 settlement
// 各子 Service 的 facade。外部调用方（如 game 模块的 GameEventHandler）应通过本
// facade 调用 settlement 用例，不直接依赖 settlement/service 下的具体 Service。
//
// settlement 模块的事务自管理在各 Repository 方法内（如 CreateBillsInTransaction、
// CreateRoundSettlementAndBills、UpdateRefundSuccessInTransaction 等），属于自包含的
// 单仓储事务，不跨多个 Repository。
type SettleAppService struct {
	roundSettleService       *service.RoundSettleService
	penaltySettlementService *service.PenaltySettlementService
	deductService            *service.DeductService
	balanceQueryService      *service.BalanceQueryService
	balanceService           *service.BalanceService
}

// NewSettleAppService 构造 SettleAppService 实例。
// 各子 Service 由 bootstrap 创建并注入，facade 仅做转发，不引入额外业务逻辑。
func NewSettleAppService(
	roundSettleService *service.RoundSettleService,
	penaltySettlementService *service.PenaltySettlementService,
	deductService *service.DeductService,
	balanceQueryService *service.BalanceQueryService,
	balanceService *service.BalanceService,
) *SettleAppService {
	return &SettleAppService{
		roundSettleService:       roundSettleService,
		penaltySettlementService: penaltySettlementService,
		deductService:            deductService,
		balanceQueryService:      balanceQueryService,
		balanceService:           balanceService,
	}
}

// SettleRound 编排单局结算用例：写 grab/commission BillRecord、触发奖励结算、
// 委托 GameSettleReportingService 完成会话级结算。
// 委托给 RoundSettleService.SettleRound，事务边界保持不变。
func (s *SettleAppService) SettleRound(ctx context.Context, req *dto.RoundSettleRequest) error {
	return s.roundSettleService.SettleRound(ctx, req)
}

// SettleGame 编排游戏级结算用例：对所有已 Credited 的回合执行会话级净额结算并上报平台。
// 委托给 RoundSettleService.SettleGame，事务边界保持不变。
func (s *SettleAppService) SettleGame(ctx context.Context, sessionID int64) error {
	return s.roundSettleService.SettleGame(ctx, sessionID)
}

// DeductPenaltyToPlatform 编排罚款扣款用例：从用户扣款上交平台。
// 委托给 PenaltySettlementService.DeductPenaltyToPlatform，事务边界保持不变。
func (s *SettleAppService) DeductPenaltyToPlatform(ctx context.Context, req *dto.PenaltyDeductRequest) error {
	return s.penaltySettlementService.DeductPenaltyToPlatform(ctx, req)
}

// DistributePenaltyFromPlatform 编排罚款分配用例：将平台罚款分配给指定接收方。
// 委托给 PenaltySettlementService.DistributePenaltyFromPlatform，事务边界保持不变。
func (s *SettleAppService) DistributePenaltyFromPlatform(ctx context.Context, req *dto.PenaltyDistributeRequest) error {
	return s.penaltySettlementService.DistributePenaltyFromPlatform(ctx, req)
}

// DeductForFirstRound 编排首回合扣款用例：创建 round_settlement + bills 并执行批量扣款。
// 委托给 DeductService.DeductForFirstRound，事务边界保持不变。
func (s *SettleAppService) DeductForFirstRound(ctx context.Context, req *dto.FirstRoundDeductRequest) (*dto.FirstRoundDeductResult, error) {
	return s.deductService.DeductForFirstRound(ctx, req)
}

// DeductForLaterRound 编排后续回合扣款用例：最低金额玩家房费扣款。
// 委托给 DeductService.DeductForLaterRound，事务边界保持不变。
func (s *SettleAppService) DeductForLaterRound(ctx context.Context, req *dto.LaterRoundDeductRequest) error {
	return s.deductService.DeductForLaterRound(ctx, req)
}

// DeductForSystemPacket 编排系统红包扣款用例：平台账户扣款。
// 委托给 DeductService.DeductForSystemPacket，事务边界保持不变。
func (s *SettleAppService) DeductForSystemPacket(ctx context.Context, req *dto.SystemPacketDeductRequest) error {
	return s.deductService.DeductForSystemPacket(ctx, req)
}

// CheckBalance 查询用户余额是否满足所需金额。
// 委托给 BalanceQueryService.CheckBalance。
func (s *SettleAppService) CheckBalance(ctx context.Context, userID int64, requiredAmount int64) (int64, bool, error) {
	return s.balanceQueryService.CheckBalance(ctx, userID, requiredAmount)
}

// CheckBalanceForReady 检查用户余额是否满足开局所需费用。
// 委托给 BalanceService.CheckBalanceForReady。
func (s *SettleAppService) CheckBalanceForReady(ctx context.Context, req *dto.BalanceCheckRequest) (*dto.BalanceCheckResult, error) {
	return s.balanceService.CheckBalanceForReady(ctx, req)
}
