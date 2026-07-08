package repository

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/model"
)

// RefundAuditRepository 退款审核仓储接口，负责 RefundAudit 的 CRUD 与状态机更新。
// 部分跨表事务方法（同时更新 RefundAudit 与 BillRecord）也归属在此接口下，
// 事务边界由 AppService 通过 DBRepository.WithTransaction 编排，Repository 自身不开事务。
type RefundAuditRepository interface {
	// GetRefundAuditByOrderNo 根据退款单号查询退款审核记录
	GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error)
	// GetRefundAuditByBillID 根据账单 id 查询 Pending 状态的退款审核记录
	GetRefundAuditByBillID(ctx context.Context, billID int64) (*model.RefundAudit, error)
	// UpdateRefundAuditStatus 乐观锁更新退款审核状态（仅允许从 Pending 转换）
	UpdateRefundAuditStatus(ctx context.Context, refundID int64, status int, approvedAt time.Time, approvedBy int64, remark string) error
	// UpdateRefundAuditToProcessing 乐观锁更新退款审核为 Processing（仅允许从 fromStatus 转换）
	UpdateRefundAuditToProcessing(ctx context.Context, refundID int64, fromStatus int) error
	// UpdateRefundAuditToPendingForRetry 原子地将退款审核状态从 fromStatus 回退到 Pending，用于重试
	UpdateRefundAuditToPendingForRetry(ctx context.Context, refundID int64, fromStatus int, errMsg string) error
	// UpdateRefundSuccess 原子地更新退款审核为 Refunded 并同步账单为 Refunded。
	// 事务边界由 AppService 通过 DBRepository.WithTransaction 编排，调用方应在事务回调内
	// 通过 tx.RefundAuditRepo() 获取基于事务连接的子 repo 后调用本方法。
	UpdateRefundSuccess(ctx context.Context, refundID int64, refundFromStatus, billFromStatus int, platformTransID string, refundedAt time.Time) error
	// CreateRefundAuditAndUpdateBillRefundStatus 创建退款审核并更新账单 refund_status。
	// 事务边界由 AppService 通过 DBRepository.WithTransaction 编排，调用方应在事务回调内
	// 通过 tx.RefundAuditRepo() 获取基于事务连接的子 repo 后调用本方法。
	CreateRefundAuditAndUpdateBillRefundStatus(ctx context.Context, refundAudit *model.RefundAudit, billID int64, fromRefundStatus, toRefundStatus int, refundOrderNo string) error
	// RejectRefund 拒绝退款审核并更新账单 refund_status。
	// 事务边界由 AppService 通过 DBRepository.WithTransaction 编排，调用方应在事务回调内
	// 通过 tx.RefundAuditRepo() 获取基于事务连接的子 repo 后调用本方法。
	RejectRefund(ctx context.Context, refundID int64, refundFromStatus int, billID int64, billFromRefundStatus, billToRefundStatus int, errMsg string) error
	// GetRefundsByStatus 分页查询指定状态的退款审核记录（按申请时间正序）
	GetRefundsByStatus(ctx context.Context, status int, limit int, offset int) ([]*model.RefundAudit, error)
}
