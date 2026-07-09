package application

import (
	"context"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/service"
)

// SchedulerAppService 是 settlement 模块 5 个 scheduler 的统一 Application 层入口。
// 各方法逻辑与原 scheduler.execute() 完全等价，仅做封装转发：scheduler 在变更 4b 后
// 不再持有 repository，循环逻辑全部内聚于本 AppService，scheduler 退化为纯薄壳，
// 仅负责定时调度、分布式锁与配置参数透传。
//
// 事务边界与错误处理保持与原 scheduler 完全一致，不引入新逻辑、不改变事务边界。
type SchedulerAppService struct {
	creditRetrySvc      *service.CreditRetryService
	gameSettleSvc       *service.GameSettleReportingService
	refundExecuteSvc    *service.RefundExecuteService
	settlementCheckSvc  *service.SettlementCheckService
	roundSettlementRepo repository.RoundSettlementRepository
	settlementQueryRepo repository.SettlementQueryRepository
	refundAuditRepo     repository.RefundAuditRepository
}

// NewSchedulerAppService 构造 SchedulerAppService 实例。
// 各子 Service 与 Repository 由 bootstrap 创建并注入，scheduler 通过本构造函数
// 持有 AppService 引用，其 execute() 仅调用对应方法并传入配置参数。
func NewSchedulerAppService(
	creditRetrySvc *service.CreditRetryService,
	gameSettleSvc *service.GameSettleReportingService,
	refundExecuteSvc *service.RefundExecuteService,
	settlementCheckSvc *service.SettlementCheckService,
	roundSettlementRepo repository.RoundSettlementRepository,
	settlementQueryRepo repository.SettlementQueryRepository,
	refundAuditRepo repository.RefundAuditRepository,
) *SchedulerAppService {
	return &SchedulerAppService{
		creditRetrySvc:      creditRetrySvc,
		gameSettleSvc:       gameSettleSvc,
		refundExecuteSvc:    refundExecuteSvc,
		settlementCheckSvc:  settlementCheckSvc,
		roundSettlementRepo: roundSettlementRepo,
		settlementQueryRepo: settlementQueryRepo,
		refundAuditRepo:     refundAuditRepo,
	}
}

// RetryCreditBills 等价于 CreditRetryScheduler.execute()：拉取可重试入账账单并逐条重试。
// limit 由 scheduler 配置透传，替代原硬编码逻辑；行为与原 scheduler 完全一致。
func (s *SchedulerAppService) RetryCreditBills(ctx context.Context, limit int) error {
	bills, err := s.creditRetrySvc.GetRetryableCredits(ctx, limit)
	if err != nil {
		logger.Error("get retryable credits failed", "error", err)
		return err
	}

	for _, bill := range bills {
		if bill.NextRetryAt != nil && bill.NextRetryAt.After(time.Now()) {
			continue
		}

		if err := s.creditRetrySvc.RetryCredit(ctx, bill.ID); err != nil {
			logger.Error("retry credit failed", "bill_id", bill.ID, "error", err)
		}
	}

	return nil
}

// RetryGameSettle 等价于 GameSettleRetryScheduler.execute()：拉取游戏级结算失败的会话，
// 对每个未结算玩家执行重试，全部成功则更新会话结算状态。
// limit 由 scheduler 配置透传，替代原硬编码 100；行为与原 scheduler 完全一致。
func (s *SchedulerAppService) RetryGameSettle(ctx context.Context, limit int) error {
	// Find games where game settle failed
	sessionIDs, err := s.roundSettlementRepo.GetFailedGameSettlements(ctx, limit)
	if err != nil {
		logger.Error("get failed game settlements failed", "error", err)
		return err
	}

	for _, sessionID := range sessionIDs {
		// Get unsettled users in this game
		userIDs, err := s.settlementQueryRepo.GetUnsettledUsersBySession(ctx, sessionID)
		if err != nil {
			logger.Error("get unsettled users failed", "session_id", sessionID, "error", err)
			continue
		}

		allSuccess := true
		for _, userID := range userIDs {
			if err := s.gameSettleSvc.RetryPlayerSettle(ctx, sessionID, userID); err != nil {
				logger.Error("retry player game settle failed", "session_id", sessionID, "user_id", userID, "error", err)
				allSuccess = false
			}
		}

		if allSuccess && len(userIDs) > 0 {
			if err := s.roundSettlementRepo.UpdateGameSettleStatusBySession(ctx, sessionID, domain.GameSettleStatusFailed, domain.GameSettleStatusSuccess); err != nil {
				logger.Error("update game settle status by session failed", "session_id", sessionID, "error", err)
			}
		}
	}

	return nil
}

// SettleGameByTimeout 等价于 GameSettleTimeoutScheduler.execute()：拉取超时未完成游戏级
// 结算的会话并强制结算。timeoutDuration 与 limit 由 scheduler 配置透传；行为与原 scheduler 完全一致。
func (s *SchedulerAppService) SettleGameByTimeout(ctx context.Context, timeoutDuration time.Duration, limit int) error {
	// 查找所有回合已入账但游戏结算超过 timeoutDuration 未完成的会话
	sessionIDs, err := s.roundSettlementRepo.GetTimedOutGameSettlements(ctx, timeoutDuration, limit)
	if err != nil {
		logger.Error("get timed out game settlements failed", "error", err)
		return err
	}

	for _, sessionID := range sessionIDs {
		if err := s.gameSettleSvc.SettleGame(ctx, sessionID); err != nil {
			logger.Error("force game settle failed", "session_id", sessionID, "error", err)
		} else {
			logger.Info("force game settle success", "session_id", sessionID)
		}
	}

	return nil
}

// ProcessPendingRefunds 等价于 RefundProcessScheduler.execute()：分页拉取待处理退款审核记录，
// 对首回合失败类型的退款执行自动审批。limit 与 offset 由 scheduler 配置透传；
// 行为与原 scheduler 完全一致。
func (s *SchedulerAppService) ProcessPendingRefunds(ctx context.Context, limit int, offset int) error {
	refunds, err := s.refundAuditRepo.GetRefundsByStatus(ctx, domain.RefundStatusPending, limit, offset)
	if err != nil {
		logger.Error("get pending refunds failed", "error", err)
		return err
	}

	for _, refund := range refunds {
		if refund.RefundType == domain.RefundTypeFirstRoundFail {
			if err := s.refundExecuteSvc.ApproveRefund(ctx, &dto.RefundApproveRequest{
				RefundOrderNo: refund.RefundOrderNo,
				ApprovedBy:    0,
			}); err != nil {
				logger.Error("approve refund failed", "refund_order_no", refund.RefundOrderNo, "error", err)
			}
		}
	}

	return nil
}

// RunSettlementCheck 等价于 SettlementCheckScheduler.execute()：依次执行首回合扣款失败
// 与已扣款未结算两项一致性检查。failedFirstRoundLookback、deductedNotSettledLookback 与 limit
// 由 scheduler 配置透传；行为与原 scheduler 完全一致。
func (s *SchedulerAppService) RunSettlementCheck(ctx context.Context, failedFirstRoundLookback time.Duration, deductedNotSettledLookback time.Duration, limit int) error {
	if err := s.settlementCheckSvc.CheckFirstRoundDeductFailure(ctx, time.Now().Add(-failedFirstRoundLookback), limit); err != nil {
		logger.Error("check first round deduct failure failed", "error", err)
		return err
	}
	if err := s.settlementCheckSvc.CheckDeductedButNotSettled(ctx, time.Now().Add(-deductedNotSettledLookback), limit); err != nil {
		logger.Error("check deducted but not settled failed", "error", err)
		return err
	}
	return nil
}
