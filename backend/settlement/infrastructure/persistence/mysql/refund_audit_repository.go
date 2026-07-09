package mysql

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// refundAuditRepository 实现 repository.RefundAuditRepository 接口，负责 RefundAudit 的 CRUD 与状态机更新。
// 代码由 BillManager 迁移而来，逻辑保持一致。
// 部分跨表事务方法（同时更新 RefundAudit 与 BillRecord）也归属在此仓储下。
// 接口层已切换为 domain.RefundAudit 聚合根，本实现层在方法边界完成 domain ↔ model 转换，
// DB 操作仍基于 model.RefundAudit（携带 GORM tag 与 TableName）。
type refundAuditRepository struct {
	db *gorm.DB
}

// NewRefundAuditRepository 创建 RefundAuditRepository 实例，返回接口类型。
func NewRefundAuditRepository(db *gorm.DB) repository.RefundAuditRepository {
	return &refundAuditRepository{db: db}
}

// 编译期断言：确保 refundAuditRepository 实现 repository.RefundAuditRepository 接口。
var _ repository.RefundAuditRepository = (*refundAuditRepository)(nil)

func (m *refundAuditRepository) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*domain.RefundAudit, error) {
	var refund model.RefundAudit
	err := m.db.WithContext(ctx).Where("refund_order_no = ?", refundOrderNo).First(&refund).Error
	if err != nil {
		return nil, err
	}
	return refundAuditModelToDomain(&refund), nil
}

func (m *refundAuditRepository) GetRefundAuditByBillID(ctx context.Context, billID int64) (*domain.RefundAudit, error) {
	var refund model.RefundAudit
	err := m.db.WithContext(ctx).Where("bill_id = ? AND status = ?", billID, domain.RefundStatusPending).
		Order("created_at desc").
		First(&refund).Error
	if err != nil {
		return nil, err
	}
	return refundAuditModelToDomain(&refund), nil
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
		Where("id = ? AND status = ?", refundID, domain.RefundStatusPending).
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
		Update("status", domain.RefundStatusProcessing)
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
			"status":        domain.RefundStatusPending,
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
			"status":            domain.RefundStatusRefunded,
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
			"refund_status":   domain.RefundStatusRefunded,
			"refund_amount":   refund.RefundAmount,
			"refund_order_no": refund.RefundOrderNo,
			"status":          domain.BillStatusRefunded,
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
// refundAudit 入参为 domain 聚合根，内部转换为 model 持久化实体后写入 DB，
// 并回填自增主键与时间戳到 domain 聚合根，保持与原 Create 行为一致。
func (m *refundAuditRepository) CreateRefundAuditAndUpdateBillRefundStatus(ctx context.Context, refundAudit *domain.RefundAudit, billID int64, fromRefundStatus, toRefundStatus int, refundOrderNo string) error {
	rModel := refundAuditDomainToModel(refundAudit)
	if err := m.db.WithContext(ctx).Create(rModel).Error; err != nil {
		return fmt.Errorf("create refund audit and update bill refund status failed: %w", err)
	}
	// 回填 DB 自动生成的字段（自增主键、时间戳）到 domain 聚合根，保持与原 Create(refundAudit) 行为一致。
	refundAudit.ID = rModel.ID
	refundAudit.CreatedAt = rModel.CreatedAt
	refundAudit.UpdatedAt = rModel.UpdatedAt

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

func (m *refundAuditRepository) GetRefundsByStatus(ctx context.Context, status int, limit int, offset int) ([]*domain.RefundAudit, error) {
	var refunds []*model.RefundAudit
	err := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("status = ?", status).
		Order("applied_at asc").
		Limit(limit).
		Offset(offset).
		Find(&refunds).Error
	if err != nil {
		return nil, err
	}
	return refundAuditModelSliceToDomain(refunds), nil
}

// refundAuditModelToDomain 将 model 层退款审核记录转换为 domain 层聚合根。
func refundAuditModelToDomain(m *model.RefundAudit) *domain.RefundAudit {
	if m == nil {
		return nil
	}
	return &domain.RefundAudit{
		ID:              m.ID,
		RefundOrderNo:   m.RefundOrderNo,
		RoundTraceID:    m.RoundTraceID,
		BatchID:         m.BatchID,
		RoomID:          m.RoomID,
		SessionID:       m.SessionID,
		RoundID:         m.RoundID,
		UserID:          m.UserID,
		BillID:          m.BillID,
		BillOrderNo:     m.BillOrderNo,
		RefundAmount:    m.RefundAmount,
		RefundReason:    m.RefundReason,
		RefundType:      m.RefundType,
		Status:          m.Status,
		AppliedAt:       m.AppliedAt,
		AppliedBy:       m.AppliedBy,
		ApprovedAt:      m.ApprovedAt,
		ApprovedBy:      m.ApprovedBy,
		ApproveRemark:   m.ApproveRemark,
		RefundedAt:      m.RefundedAt,
		PlatformTransID: m.PlatformTransID,
		ErrorMessage:    m.ErrorMessage,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

// refundAuditDomainToModel 将 domain 层聚合根转换为 model 层持久化实体。
func refundAuditDomainToModel(d *domain.RefundAudit) *model.RefundAudit {
	if d == nil {
		return nil
	}
	return &model.RefundAudit{
		ID:              d.ID,
		RefundOrderNo:   d.RefundOrderNo,
		RoundTraceID:    d.RoundTraceID,
		BatchID:         d.BatchID,
		RoomID:          d.RoomID,
		SessionID:       d.SessionID,
		RoundID:         d.RoundID,
		UserID:          d.UserID,
		BillID:          d.BillID,
		BillOrderNo:     d.BillOrderNo,
		RefundAmount:    d.RefundAmount,
		RefundReason:    d.RefundReason,
		RefundType:      d.RefundType,
		Status:          d.Status,
		AppliedAt:       d.AppliedAt,
		AppliedBy:       d.AppliedBy,
		ApprovedAt:      d.ApprovedAt,
		ApprovedBy:      d.ApprovedBy,
		ApproveRemark:   d.ApproveRemark,
		RefundedAt:      d.RefundedAt,
		PlatformTransID: d.PlatformTransID,
		ErrorMessage:    d.ErrorMessage,
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
	}
}

// refundAuditModelSliceToDomain 将 model 层退款审核切片转换为 domain 层聚合根切片。
func refundAuditModelSliceToDomain(ms []*model.RefundAudit) []*domain.RefundAudit {
	if ms == nil {
		return nil
	}
	ds := make([]*domain.RefundAudit, 0, len(ms))
	for _, m := range ms {
		ds = append(ds, refundAuditModelToDomain(m))
	}
	return ds
}
