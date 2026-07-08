package mysql

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// refundAuditRepository 实现 domain.RefundAuditRepository 接口，负责 RefundAudit 的 CRUD 与状态机更新。
// 代码由 BillManager 迁移而来，逻辑保持一致。
// 部分跨表事务方法（同时更新 RefundAudit 与 BillRecord）也归属在此仓储下。
type refundAuditRepository struct {
	db *gorm.DB
}

// NewRefundAuditRepository 创建 RefundAuditRepository 实例，返回接口类型。
func NewRefundAuditRepository(db *gorm.DB) domain.RefundAuditRepository {
	return &refundAuditRepository{db: db}
}

// 编译期断言：确保 refundAuditRepository 实现 domain.RefundAuditRepository 接口。
var _ domain.RefundAuditRepository = (*refundAuditRepository)(nil)

func (m *refundAuditRepository) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error) {
	var refund model.RefundAudit
	err := m.db.WithContext(ctx).Where("refund_order_no = ?", refundOrderNo).First(&refund).Error
	if err != nil {
		return nil, err
	}
	return &refund, nil
}

func (m *refundAuditRepository) GetRefundAuditByBillID(ctx context.Context, billID int64) (*model.RefundAudit, error) {
	var refund model.RefundAudit
	err := m.db.WithContext(ctx).Where("bill_id = ? AND status = ?", billID, dto.RefundStatusPending).
		Order("created_at desc").
		First(&refund).Error
	if err != nil {
		return nil, err
	}
	return &refund, nil
}

func (m *refundAuditRepository) UpdateRefundAuditStatus(ctx context.Context, refundID int64, status int, approvedAt time.Time, approvedBy int64, remark string) error {
	updates := map[string]interface{}{
		"status":         status,
		"approved_at":    approvedAt,
		"approved_by":    approvedBy,
		"approve_remark": remark,
	}
	// 乐观锁：只允许从 Pending 状态转换，防止并发审批/拒绝同一退款单。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("id = ? AND status = ?", refundID, dto.RefundStatusPending).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update refund audit status failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 Pending（已被审批/拒绝/退款完成），视为幂等成功
		return nil
	}
	return nil
}

func (m *refundAuditRepository) UpdateRefundAuditToProcessing(ctx context.Context, refundID int64, fromStatus int) error {
	// 乐观锁：只允许从 fromStatus 状态转换到 Processing，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("id = ? AND status = ?", refundID, fromStatus).
		Update("status", dto.RefundStatusProcessing)
	if result.Error != nil {
		return fmt.Errorf("update refund audit to processing failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 fromStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

// UpdateRefundAuditToPendingForRetry 原子地将 refund_audit 的 status 从 fromStatus 回退到 Pending，
// 同时更新 error_message。用于 executeRefund 失败后使退款单能被 RefundProcessScheduler 重新查到并重试。
// 乐观锁：只允许从 fromStatus 状态转换，防止并发覆盖。
// RowsAffected == 0 表示已被其他事务处理，视为幂等成功。
func (m *refundAuditRepository) UpdateRefundAuditToPendingForRetry(ctx context.Context, refundID int64, fromStatus int, errMsg string) error {
	result := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("id = ? AND status = ?", refundID, fromStatus).
		Updates(map[string]interface{}{
			"status":        dto.RefundStatusPending,
			"error_message": errMsg,
		})
	if result.Error != nil {
		return fmt.Errorf("update refund audit to pending for retry failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 fromStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

// UpdateRefundSuccess 原子地更新退款审核为 Refunded 并同步账单为 Refunded。
// 事务边界由 AppService 通过 DBRepository.WithTransaction 编排：在事务回调内通过
// tx.RefundAuditRepo() 获取的子 repo，其 m.db 即为事务连接，refund_audit 与 bill_record
// 的更新自动纳入同一事务。
func (m *refundAuditRepository) UpdateRefundSuccess(ctx context.Context, refundID int64, refundFromStatus, billFromStatus int, platformTransID string, refundedAt time.Time) error {
	// 乐观锁：refund_audit 只允许从 refundFromStatus 转换到 Refunded。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("id = ? AND status = ?", refundID, refundFromStatus).
		Updates(map[string]interface{}{
			"status":            dto.RefundStatusRefunded,
			"refunded_at":       refundedAt,
			"platform_trans_id": platformTransID,
		})
	if result.Error != nil {
		return fmt.Errorf("update refund audit to refunded failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 退款单已不是 refundFromStatus（已被其他事务处理），视为幂等成功
		return nil
	}

	var refund model.RefundAudit
	if err := m.db.WithContext(ctx).Where("id = ?", refundID).First(&refund).Error; err != nil {
		return fmt.Errorf("get refund audit failed: %w", err)
	}

	// 乐观锁：bill 只允许从 billFromStatus 转换到 Refunded。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	billResult := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ? AND status = ?", refund.BillID, billFromStatus).
		Updates(map[string]interface{}{
			"refund_status":   dto.RefundStatusRefunded,
			"refund_amount":   refund.RefundAmount,
			"refund_order_no": refund.RefundOrderNo,
			"status":          dto.BillStatusRefunded,
		})
	if billResult.Error != nil {
		return fmt.Errorf("update bill to refunded failed: %w", billResult.Error)
	}
	if billResult.RowsAffected == 0 {
		// 账单已不是 billFromStatus（已被其他事务处理），视为幂等成功
		return nil
	}

	return nil
}

// CreateRefundAuditAndUpdateBillRefundStatus 创建退款审核并更新账单 refund_status。
// 事务边界由 AppService 通过 DBRepository.WithTransaction 编排：在事务回调内通过
// tx.RefundAuditRepo() 获取的子 repo，其 m.db 即为事务连接，两步操作自动纳入同一事务。
func (m *refundAuditRepository) CreateRefundAuditAndUpdateBillRefundStatus(ctx context.Context, refundAudit *model.RefundAudit, billID int64, fromRefundStatus, toRefundStatus int, refundOrderNo string) error {
	if err := m.db.WithContext(ctx).Create(refundAudit).Error; err != nil {
		return fmt.Errorf("create refund audit and update bill refund status failed: %w", err)
	}

	// 乐观锁：只允许从 fromRefundStatus 转换，防止并发覆盖。
	// RowsAffected == 0 表示已被其他事务处理，视为幂等成功。
	result := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ? AND refund_status = ?", billID, fromRefundStatus).
		Updates(map[string]interface{}{
			"refund_status":   toRefundStatus,
			"refund_order_no": refundOrderNo,
		})
	if result.Error != nil {
		return fmt.Errorf("create refund audit and update bill refund status failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 fromRefundStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

// RejectRefund 拒绝退款审核并更新账单 refund_status。
// 事务边界由 AppService 通过 DBRepository.WithTransaction 编排：在事务回调内通过
// tx.RefundAuditRepo() 获取的子 repo，其 m.db 即为事务连接，两步操作自动纳入同一事务。
func (m *refundAuditRepository) RejectRefund(ctx context.Context, refundID int64, refundFromStatus int, billID int64, billFromRefundStatus, billToRefundStatus int, errMsg string) error {
	// 乐观锁：refund_audit 只允许从 refundFromStatus 转换到 Rejected，防止并发覆盖。
	// RowsAffected == 0 表示已被其他事务处理，视为幂等成功。
	result := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("id = ? AND status = ?", refundID, refundFromStatus).
		Updates(map[string]interface{}{
			"status":         dto.RefundStatusRejected,
			"approved_at":    time.Now(),
			"approve_remark": errMsg,
		})
	if result.Error != nil {
		return fmt.Errorf("reject refund failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 退款单已不是 refundFromStatus（已被其他事务处理），视为幂等成功
		return nil
	}

	// 乐观锁：bill 只允许从 billFromRefundStatus 转换，防止并发覆盖。
	// RowsAffected == 0 表示已被其他事务处理，视为幂等成功。
	billResult := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ? AND refund_status = ?", billID, billFromRefundStatus).
		Updates(map[string]interface{}{
			"refund_status": billToRefundStatus,
		})
	if billResult.Error != nil {
		return fmt.Errorf("reject refund failed: %w", billResult.Error)
	}
	if billResult.RowsAffected == 0 {
		// 账单已不是 billFromRefundStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

func (m *refundAuditRepository) GetRefundsByStatus(ctx context.Context, status int, limit int, offset int) ([]*model.RefundAudit, error) {
	var refunds []*model.RefundAudit
	err := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("status = ?", status).
		Order("applied_at asc").
		Limit(limit).
		Offset(offset).
		Find(&refunds).Error
	return refunds, err
}
