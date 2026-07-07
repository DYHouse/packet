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

// billRepository 实现 domain.BillRepository 接口，负责 BillRecord 的 CRUD 与状态机更新。
// 代码由 BillManager 迁移而来，逻辑保持一致。
type billRepository struct {
	db *gorm.DB
}

// NewBillRepository 创建 BillRepository 实例，返回接口类型。
func NewBillRepository(db *gorm.DB) domain.BillRepository {
	return &billRepository{db: db}
}

// 编译期断言：确保 billRepository 实现 domain.BillRepository 接口。
var _ domain.BillRepository = (*billRepository)(nil)

func (m *billRepository) CreateBill(ctx context.Context, bill *model.BillRecord) error {
	if err := m.db.WithContext(ctx).Create(bill).Error; err != nil {
		return fmt.Errorf("create bill failed: %w", err)
	}
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
			"status":         dto.BillStatusSuccess,
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

func (m *billRepository) GetBillByTraceID(ctx context.Context, traceID string) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("round_trace_id = ?", traceID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *billRepository) GetBillByID(ctx context.Context, billID int64) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("id = ?", billID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *billRepository) ExistsByRoundAndType(ctx context.Context, roundID int64, billType int) (bool, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("round_id = ? AND bill_type = ?", roundID, billType).
		Count(&count).Error
	return count > 0, err
}

func (m *billRepository) GetBillByRoundTypeAndUser(ctx context.Context, roundID int64, billType int, userID int64) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("round_id = ? AND bill_type = ? AND user_id = ?", roundID, billType, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

// GetBillByTraceTypeAndUser 基于 round_trace_id 查询 Bill，用于 RoundID 未设置的场景（如惩罚扣款）。
func (m *billRepository) GetBillByTraceTypeAndUser(ctx context.Context, roundTraceID string, billType int, userID int64) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("round_trace_id = ? AND bill_type = ? AND user_id = ?", roundTraceID, billType, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *billRepository) GetBillsByTraceID(ctx context.Context, traceID string) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("round_trace_id = ?", traceID).Find(&bills).Error
	return bills, err
}

func (m *billRepository) GetBillsByUserID(ctx context.Context, userID int64, limit, offset int) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at desc").
		Limit(limit).
		Offset(offset).
		Find(&bills).Error
	return bills, err
}

func (m *billRepository) GetBillsByRoundID(ctx context.Context, roundID int64) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("round_id = ?", roundID).
		Order("created_at asc").
		Find(&bills).Error
	return bills, err
}

func (m *billRepository) CreateBillsInTransaction(ctx context.Context, bills []*model.BillRecord) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, bill := range bills {
			if err := tx.Create(bill).Error; err != nil {
				return fmt.Errorf("create bill failed: %w", err)
			}
		}
		return nil
	})
}

func (m *billRepository) CreateBillsPairInTransaction(ctx context.Context, bill1 *model.BillRecord, bill2 *model.BillRecord) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(bill1).Error; err != nil {
			return fmt.Errorf("create bill failed: %w", err)
		}
		if err := tx.Create(bill2).Error; err != nil {
			return fmt.Errorf("create bill failed: %w", err)
		}
		return nil
	})
}

func (m *billRepository) GetBillsByBatchID(ctx context.Context, batchID string) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("batch_id = ?", batchID).
		Order("created_at asc").
		Find(&bills).Error
	return bills, err
}

func (m *billRepository) GetBillByBatchAndUser(ctx context.Context, batchID string, userID int64) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("batch_id = ? AND user_id = ?", batchID, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *billRepository) GetRetryableCredits(ctx context.Context, limit int) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("amount > 0 AND status IN (?) AND retry_count < ?",
			[]int{dto.BillStatusProcessing, dto.BillStatusFailed}, dto.MaxRetryCount).
		Where("next_retry_at IS NULL OR next_retry_at <= ?", time.Now()).
		Order("created_at asc").
		Limit(limit).
		Find(&bills).Error
	return bills, err
}

func (m *billRepository) IncrementRetryCountWithNextRetryTime(ctx context.Context, billID int64, nextRetryAt time.Time) error {
	if err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Updates(map[string]interface{}{
			"retry_count":   gorm.Expr("retry_count + 1"),
			"next_retry_at": nextRetryAt,
		}).Error; err != nil {
		return fmt.Errorf("increment retry count failed: %w", err)
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
func (m *billRepository) GetBillsBySessionTypeAndUser(ctx context.Context, sessionID int64, billType int, userID int64) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("session_id = ? AND bill_type = ? AND user_id = ?", sessionID, billType, userID).Find(&bills).Error
	return bills, err
}

// UpdateGameSettleStatusByUser 标记该玩家在该游戏中所有 Bill 的游戏级结算状态
func (m *billRepository) UpdateGameSettleStatusByUser(ctx context.Context, sessionID int64, userID int64, fromStatus, toStatus int) error {
	now := time.Now()
	updates := map[string]interface{}{
		"game_settle_status": toStatus,
	}
	if toStatus == dto.BillGameSettleSettled {
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
