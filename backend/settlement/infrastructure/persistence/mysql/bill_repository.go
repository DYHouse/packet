package mysql

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/settlement/domain"
	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// billRepository 实现 repository.BillRepository 接口，负责 BillRecord 的 CRUD 与状态机更新。
// 代码由 BillManager 迁移而来，逻辑保持一致。
// 接口层已切换为 domain.BillRecord 聚合根，本实现层在方法边界完成 domain ↔ model 转换，
// DB 操作仍基于 model.BillRecord（携带 GORM tag 与 TableName）。
type billRepository struct {
	db *gorm.DB
}

// NewBillRepository 创建 BillRepository 实例，返回接口类型。
func NewBillRepository(db *gorm.DB) repository.BillRepository {
	return &billRepository{db: db}
}

// 编译期断言：确保 billRepository 实现 repository.BillRepository 接口。
var _ repository.BillRepository = (*billRepository)(nil)

func (m *billRepository) CreateBill(ctx context.Context, bill *domain.BillRecord) error {
	mModel := billDomainToModel(bill)
	if err := m.db.WithContext(ctx).Create(mModel).Error; err != nil {
		return fmt.Errorf("create bill failed: %w", err)
	}
	// 回填 DB 自动生成的字段（自增主键、时间戳）到 domain 聚合根，保持与原 Create(bill) 行为一致。
	bill.ID = mModel.ID
	bill.CreatedAt = mModel.CreatedAt
	bill.UpdatedAt = mModel.UpdatedAt
	return nil
}

func (m *billRepository) UpdateBillStatus(ctx context.Context, billID int64, fromStatus, toStatus int, errMsg string) error {
	updates := map[string]interface{}{
		"status": toStatus,
	}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	// 乐观锁：只允许从 fromStatus 转换，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ? AND status = ?", billID, fromStatus).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update bill status failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 fromStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

func (m *billRepository) UpdateBillSuccess(ctx context.Context, billID int64, fromStatus int, balanceBefore, balanceAfter int64) error {
	// 乐观锁：只允许从 fromStatus 转换到 Success，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ? AND status = ?", billID, fromStatus).
		Updates(map[string]interface{}{
			"status":         domain.BillStatusSuccess,
			"balance_before": balanceBefore,
			"balance_after":  balanceAfter,
		})
	if result.Error != nil {
		return fmt.Errorf("update bill success failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 fromStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

func (m *billRepository) GetBillByID(ctx context.Context, billID int64) (*domain.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("id = ?", billID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return billModelToDomain(&bill), nil
}

func (m *billRepository) ExistsByRoundAndType(ctx context.Context, roundID int64, billType int) (bool, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("round_id = ? AND bill_type = ?", roundID, billType).
		Count(&count).Error
	return count > 0, err
}

func (m *billRepository) GetBillByRoundTypeAndUser(ctx context.Context, roundID int64, billType int, userID int64) (*domain.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("round_id = ? AND bill_type = ? AND user_id = ?", roundID, billType, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return billModelToDomain(&bill), nil
}

// GetBillByTraceTypeAndUser 基于 round_trace_id 查询 Bill，用于 RoundID 未设置的场景（如惩罚扣款）。
func (m *billRepository) GetBillByTraceTypeAndUser(ctx context.Context, roundTraceID string, billType int, userID int64) (*domain.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("round_trace_id = ? AND bill_type = ? AND user_id = ?", roundTraceID, billType, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return billModelToDomain(&bill), nil
}

func (m *billRepository) GetBillsByTraceID(ctx context.Context, traceID string) ([]*domain.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("round_trace_id = ?", traceID).Find(&bills).Error
	if err != nil {
		return nil, err
	}
	return billModelSliceToDomain(bills), nil
}

func (m *billRepository) GetBillsByRoundID(ctx context.Context, roundID int64) ([]*domain.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("round_id = ?", roundID).
		Order("created_at asc").
		Find(&bills).Error
	if err != nil {
		return nil, err
	}
	return billModelSliceToDomain(bills), nil
}

// CreateBills 批量创建账单。事务边界由 AppService 通过 DBRepository.WithTransaction 编排：
// 在事务回调内通过 tx.BillRepo() 获取的子 repo，其 m.db 即为事务连接，本方法的多次 Create 自动纳入同一事务。
func (m *billRepository) CreateBills(ctx context.Context, bills []*domain.BillRecord) error {
	for _, bill := range bills {
		mModel := billDomainToModel(bill)
		if err := m.db.WithContext(ctx).Create(mModel).Error; err != nil {
			return fmt.Errorf("create bill failed: %w", err)
		}
		// 回填 DB 自动生成的字段到 domain 聚合根，保持与原 Create(bill) 行为一致。
		bill.ID = mModel.ID
		bill.CreatedAt = mModel.CreatedAt
		bill.UpdatedAt = mModel.UpdatedAt
	}
	return nil
}

// CreateBillsPair 创建两条配对账单。事务边界同 CreateBills。
func (m *billRepository) CreateBillsPair(ctx context.Context, bill1 *domain.BillRecord, bill2 *domain.BillRecord) error {
	m1 := billDomainToModel(bill1)
	if err := m.db.WithContext(ctx).Create(m1).Error; err != nil {
		return fmt.Errorf("create bill failed: %w", err)
	}
	bill1.ID = m1.ID
	bill1.CreatedAt = m1.CreatedAt
	bill1.UpdatedAt = m1.UpdatedAt

	m2 := billDomainToModel(bill2)
	if err := m.db.WithContext(ctx).Create(m2).Error; err != nil {
		return fmt.Errorf("create bill failed: %w", err)
	}
	bill2.ID = m2.ID
	bill2.CreatedAt = m2.CreatedAt
	bill2.UpdatedAt = m2.UpdatedAt
	return nil
}

func (m *billRepository) GetBillsByBatchID(ctx context.Context, batchID string) ([]*domain.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("batch_id = ?", batchID).
		Order("created_at asc").
		Find(&bills).Error
	if err != nil {
		return nil, err
	}
	return billModelSliceToDomain(bills), nil
}

func (m *billRepository) GetBillByBatchAndUser(ctx context.Context, batchID string, userID int64) (*domain.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("batch_id = ? AND user_id = ?", batchID, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return billModelToDomain(&bill), nil
}

func (m *billRepository) GetRetryableCredits(ctx context.Context, limit int) ([]*domain.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("amount > 0 AND status IN (?) AND retry_count < ?",
			[]int{domain.BillStatusProcessing, domain.BillStatusFailed}, dto.MaxRetryCount).
		Where("next_retry_at IS NULL OR next_retry_at <= ?", time.Now()).
		Order("created_at asc").
		Limit(limit).
		Find(&bills).Error
	if err != nil {
		return nil, err
	}
	return billModelSliceToDomain(bills), nil
}

func (m *billRepository) IncrementRetryCountWithNextRetryTime(ctx context.Context, billID int64, currentRetryCount int, nextRetryAt time.Time) error {
	// 乐观锁：通过 currentRetryCount 作为条件（WHERE id=? AND retry_count=?），防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务更新。
	result := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ? AND retry_count = ?", billID, currentRetryCount).
		Updates(map[string]interface{}{
			"retry_count":   gorm.Expr("retry_count + 1"),
			"next_retry_at": nextRetryAt,
		})
	if result.Error != nil {
		return fmt.Errorf("increment retry count failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 并发冲突或记录不存在：其他线程已更新 retry_count
		return fmt.Errorf("increment retry count failed: bill_id=%d, expected_retry_count=%d, rows_affected=0", billID, currentRetryCount)
	}
	return nil
}

func (m *billRepository) UpdateBillExceptionID(ctx context.Context, billID, exceptionID int64) error {
	if err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Update("exception_id", exceptionID).Error; err != nil {
		return fmt.Errorf("update bill exception id failed: %w", err)
	}
	return nil
}

// GetBillsBySessionTypeAndUser 查询指定会话、类型、用户的账单（用于幂等检查）
func (m *billRepository) GetBillsBySessionTypeAndUser(ctx context.Context, sessionID int64, billType int, userID int64) ([]*domain.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("session_id = ? AND bill_type = ? AND user_id = ?", sessionID, billType, userID).Find(&bills).Error
	if err != nil {
		return nil, err
	}
	return billModelSliceToDomain(bills), nil
}

// UpdateGameSettleStatusByUser 标记该玩家在该游戏中所有 Bill 的游戏级结算状态
func (m *billRepository) UpdateGameSettleStatusByUser(ctx context.Context, sessionID int64, userID int64, fromStatus, toStatus int) error {
	now := time.Now()
	updates := map[string]interface{}{
		"game_settle_status": toStatus,
	}
	if toStatus == domain.BillGameSettleSettled {
		updates["game_settled_at"] = now
	}
	// 乐观锁：只允许从 fromStatus 转换，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("session_id = ? AND user_id = ? AND game_settle_status = ?", sessionID, userID, fromStatus).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update game settle status by user failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 fromStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

// billModelToDomain 将 model 层账单记录转换为 domain 层聚合根。
func billModelToDomain(m *model.BillRecord) *domain.BillRecord {
	if m == nil {
		return nil
	}
	return &domain.BillRecord{
		ID:               m.ID,
		RoundTraceID:     m.RoundTraceID,
		BizOrderNo:       m.BizOrderNo,
		PlatformTransID:  m.PlatformTransID,
		BillType:         m.BillType,
		DeductScene:      m.DeductScene,
		RoomID:           m.RoomID,
		SessionID:        m.SessionID,
		RoundID:          m.RoundID,
		RoundNo:          m.RoundNo,
		UserID:           m.UserID,
		BatchID:          m.BatchID,
		Amount:           m.Amount,
		BalanceBefore:    m.BalanceBefore,
		BalanceAfter:     m.BalanceAfter,
		Status:           m.Status,
		ReconcileStatus:  m.ReconcileStatus,
		RefundStatus:     m.RefundStatus,
		RefundOrderNo:    m.RefundOrderNo,
		RefundAmount:     m.RefundAmount,
		RefundReason:     m.RefundReason,
		RefundAppliedAt:  m.RefundAppliedAt,
		RefundApprovedAt: m.RefundApprovedAt,
		RefundApprovedBy: m.RefundApprovedBy,
		RetryCount:       m.RetryCount,
		NextRetryAt:      m.NextRetryAt,
		ErrorCode:        m.ErrorCode,
		ErrorMessage:     m.ErrorMessage,
		ExceptionID:      m.ExceptionID,
		GameSettleStatus: m.GameSettleStatus,
		GameSettledAt:    m.GameSettledAt,
		Remark:           m.Remark,
		IsRobot:          m.IsRobot,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

// billDomainToModel 将 domain 层聚合根转换为 model 层持久化实体。
func billDomainToModel(d *domain.BillRecord) *model.BillRecord {
	if d == nil {
		return nil
	}
	return &model.BillRecord{
		ID:               d.ID,
		RoundTraceID:     d.RoundTraceID,
		BizOrderNo:       d.BizOrderNo,
		PlatformTransID:  d.PlatformTransID,
		BillType:         d.BillType,
		DeductScene:      d.DeductScene,
		RoomID:           d.RoomID,
		SessionID:        d.SessionID,
		RoundID:          d.RoundID,
		RoundNo:          d.RoundNo,
		UserID:           d.UserID,
		BatchID:          d.BatchID,
		Amount:           d.Amount,
		BalanceBefore:    d.BalanceBefore,
		BalanceAfter:     d.BalanceAfter,
		Status:           d.Status,
		ReconcileStatus:  d.ReconcileStatus,
		RefundStatus:     d.RefundStatus,
		RefundOrderNo:    d.RefundOrderNo,
		RefundAmount:     d.RefundAmount,
		RefundReason:     d.RefundReason,
		RefundAppliedAt:  d.RefundAppliedAt,
		RefundApprovedAt: d.RefundApprovedAt,
		RefundApprovedBy: d.RefundApprovedBy,
		RetryCount:       d.RetryCount,
		NextRetryAt:      d.NextRetryAt,
		ErrorCode:        d.ErrorCode,
		ErrorMessage:     d.ErrorMessage,
		ExceptionID:      d.ExceptionID,
		GameSettleStatus: d.GameSettleStatus,
		GameSettledAt:    d.GameSettledAt,
		Remark:           d.Remark,
		IsRobot:          d.IsRobot,
		CreatedAt:        d.CreatedAt,
		UpdatedAt:        d.UpdatedAt,
	}
}

// billModelSliceToDomain 将 model 层账单切片转换为 domain 层聚合根切片。
func billModelSliceToDomain(ms []*model.BillRecord) []*domain.BillRecord {
	if ms == nil {
		return nil
	}
	ds := make([]*domain.BillRecord, 0, len(ms))
	for _, m := range ms {
		ds = append(ds, billModelToDomain(m))
	}
	return ds
}
