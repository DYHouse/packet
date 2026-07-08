package application

import (
	"context"

	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
	"github.com/cashparty/backend/settlement/service"
)

// RefundAppService 是 settlement 模块 Application 层的退款用例入口，作为
// RefundService 的纯转发 facade。外部调用方应通过本 facade 调用退款用例，
// 不直接依赖 settlement/service 下的具体 Service。
//
// 事务编排策略：退款用例的事务由 RefundService 内部通过 dbRepo.WithTransaction
// 编排跨表原子操作（refund_audit + bill_record），AppService 仅做纯转发，
// 不引入新逻辑、不新增事务边界（短事务原则：禁止事务内 RPC）。
type RefundAppService struct {
	refundService *service.RefundService
}

// NewRefundAppService 构造 RefundAppService 实例。
// refundService 由 bootstrap 创建并注入，承载退款用例的实际逻辑与事务编排。
func NewRefundAppService(refundService *service.RefundService) *RefundAppService {
	return &RefundAppService{
		refundService: refundService,
	}
}

// ApplyForRefund 申请退款，创建退款单并锁定账单。
// 转发到 refundService.ApplyForRefund，事务由 Service 内部编排。
// 返回退款单号；若账单状态不符或已退款，返回错误。
func (s *RefundAppService) ApplyForRefund(ctx context.Context, req *dto.RefundApplyRequest) (string, error) {
	return s.refundService.ApplyForRefund(ctx, req)
}

// ApproveRefund 审批通过退款申请并执行平台退款。
// 转发到 refundService.ApproveRefund，事务由 Service 内部编排。
// 若退款单状态非 pending，返回错误。
func (s *RefundAppService) ApproveRefund(ctx context.Context, req *dto.RefundApproveRequest) error {
	return s.refundService.ApproveRefund(ctx, req)
}

// RejectRefund 驳回退款申请，将退款单与账单状态回滚。
// 转发到 refundService.RejectRefund，事务由 Service 内部编排。
// 若退款单状态非 pending，返回错误。
func (s *RefundAppService) RejectRefund(ctx context.Context, req *dto.RefundRejectRequest) error {
	return s.refundService.RejectRefund(ctx, req)
}

// GetRefundAuditByOrderNo 按退款单号查询退款审批记录。
// 只读用例，无需事务，直接转发。
func (s *RefundAppService) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error) {
	return s.refundService.GetRefundAuditByOrderNo(ctx, refundOrderNo)
}

// GetRefundsByStatus 按状态分页查询退款审批记录列表。
// 只读用例，无需事务，直接转发。
func (s *RefundAppService) GetRefundsByStatus(ctx context.Context, status int, limit, offset int) ([]*model.RefundAudit, error) {
	return s.refundService.GetRefundsByStatus(ctx, status, limit, offset)
}
