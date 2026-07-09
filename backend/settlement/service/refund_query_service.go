package service

import (
	"context"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
)

// RefundQueryService 负责退款记录查询，只读用例，无需事务。
type RefundQueryService struct {
	refundAuditRepo repository.RefundAuditRepository
}

// NewRefundQueryService 构造 RefundQueryService 实例。
func NewRefundQueryService(refundAuditRepo repository.RefundAuditRepository) *RefundQueryService {
	return &RefundQueryService{refundAuditRepo: refundAuditRepo}
}

// GetRefundAuditByOrderNo 根据退款单号查询退款审核记录。
func (s *RefundQueryService) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*domain.RefundAudit, error) {
	return s.refundAuditRepo.GetRefundAuditByOrderNo(ctx, refundOrderNo)
}

// GetRefundsByStatus 按状态分页查询退款审核记录。
func (s *RefundQueryService) GetRefundsByStatus(ctx context.Context, status int, limit, offset int) ([]*domain.RefundAudit, error) {
	return s.refundAuditRepo.GetRefundsByStatus(ctx, status, limit, offset)
}
