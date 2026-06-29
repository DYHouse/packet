package service

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

type BillManager struct {
	db *gorm.DB
}

func NewBillManager(db *gorm.DB) *BillManager {
	return &BillManager{db: db}
}

func (m *BillManager) CreateBill(ctx context.Context, bill *model.BillRecord) error {
	return m.db.WithContext(ctx).Create(bill).Error
}

func (m *BillManager) UpdateBillStatus(ctx context.Context, billID int64, status int, errMsg string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	return m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Updates(updates).Error
}

func (m *BillManager) UpdateBillSuccess(ctx context.Context, billID int64, balanceBefore, balanceAfter int64) error {
	return m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Updates(map[string]interface{}{
			"status":         dto.BillStatusSuccess,
			"balance_before": balanceBefore,
			"balance_after":  balanceAfter,
		}).Error
}

func (m *BillManager) GetBillByTraceID(ctx context.Context, traceID string) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("round_trace_id = ?", traceID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *BillManager) GetBillByID(ctx context.Context, billID int64) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("id = ?", billID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *BillManager) ExistsByRoundAndType(ctx context.Context, roundID int64, billType int) (bool, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("round_id = ? AND bill_type = ?", roundID, billType).
		Count(&count).Error
	return count > 0, err
}

func (m *BillManager) GetBillByRoundTypeAndUser(ctx context.Context, roundID int64, billType int, userID int64) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("round_id = ? AND bill_type = ? AND user_id = ?", roundID, billType, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *BillManager) ExistsRoundSettlement(ctx context.Context, roundID int64) (bool, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_id = ?", roundID).
		Count(&count).Error
	return count > 0, err
}

func (m *BillManager) UpdateRoundSettlementCredited(ctx context.Context, traceID string, settleAmount int64, settleUserCount int, settledAt *time.Time) error {
	return m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ?", traceID).
		Updates(map[string]interface{}{
			"status":               dto.RoundStatusCredited,
			"settle_amount":        settleAmount,
			"settle_user_count":    settleUserCount,
			"settle_success_count": settleUserCount,
			"settled_at":           settledAt,
		}).Error
}

func (m *BillManager) UpdateRoundSettlementStatus(ctx context.Context, traceID string, status int, errMsg string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	return m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ?", traceID).
		Updates(updates).Error
}

func (m *BillManager) GetRoundSettlementByRoundID(ctx context.Context, roundID int64) (*model.RoundSettlement, error) {
	var settlement model.RoundSettlement
	err := m.db.WithContext(ctx).Where("round_id = ?", roundID).First(&settlement).Error
	if err != nil {
		return nil, err
	}
	return &settlement, nil
}

func (m *BillManager) GetBillsByTraceID(ctx context.Context, traceID string) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("round_trace_id = ?", traceID).Find(&bills).Error
	return bills, err
}

func (m *BillManager) GetBillsByUserID(ctx context.Context, userID int64, limit, offset int) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at desc").
		Limit(limit).
		Offset(offset).
		Find(&bills).Error
	return bills, err
}

func (m *BillManager) GetBillsByRoundID(ctx context.Context, roundID int64) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("round_id = ?", roundID).
		Order("created_at asc").
		Find(&bills).Error
	return bills, err
}

func (m *BillManager) CreateBillsInTransaction(ctx context.Context, bills []*model.BillRecord) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, bill := range bills {
			if err := tx.Create(bill).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *BillManager) CreateRoundSettlementAndBills(ctx context.Context, settlement *model.RoundSettlement, bills []*model.BillRecord) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(settlement).Error; err != nil {
			return err
		}
		for _, bill := range bills {
			if err := tx.Create(bill).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *BillManager) CreateBillsPairInTransaction(ctx context.Context, bill1 *model.BillRecord, bill2 *model.BillRecord) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(bill1).Error; err != nil {
			return err
		}
		if err := tx.Create(bill2).Error; err != nil {
			return err
		}
		return nil
	})
}

func (m *BillManager) GetBillsByBatchID(ctx context.Context, batchID string) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("batch_id = ?", batchID).
		Order("created_at asc").
		Find(&bills).Error
	return bills, err
}

func (m *BillManager) UpdateBillRefundStatus(ctx context.Context, billID int64, refundStatus int, refundOrderNo string) error {
	updates := map[string]interface{}{
		"refund_status":   refundStatus,
		"refund_order_no": refundOrderNo,
	}
	return m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Updates(updates).Error
}

func (m *BillManager) CreateRefundAudit(ctx context.Context, refund *model.RefundAudit) error {
	return m.db.WithContext(ctx).Create(refund).Error
}

func (m *BillManager) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error) {
	var refund model.RefundAudit
	err := m.db.WithContext(ctx).Where("refund_order_no = ?", refundOrderNo).First(&refund).Error
	if err != nil {
		return nil, err
	}
	return &refund, nil
}

func (m *BillManager) GetRefundAuditByBillID(ctx context.Context, billID int64) (*model.RefundAudit, error) {
	var refund model.RefundAudit
	err := m.db.WithContext(ctx).Where("bill_id = ? AND status = ?", billID, dto.RefundStatusPending).
		Order("created_at desc").
		First(&refund).Error
	if err != nil {
		return nil, err
	}
	return &refund, nil
}

func (m *BillManager) UpdateRefundAuditStatus(ctx context.Context, refundID int64, status int, approvedAt time.Time, approvedBy int64, remark string) error {
	updates := map[string]interface{}{
		"status":         status,
		"approved_at":    approvedAt,
		"approved_by":    approvedBy,
		"approve_remark": remark,
	}
	return m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("id = ?", refundID).
		Updates(updates).Error
}

func (m *BillManager) UpdateRefundAuditError(ctx context.Context, refundID int64, errMsg string) error {
	return m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("id = ?", refundID).
		Update("error_message", errMsg).Error
}

func (m *BillManager) UpdateRefundSuccessInTransaction(ctx context.Context, refundID int64, platformTransID string, refundedAt time.Time) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.RefundAudit{}).
			Where("id = ?", refundID).
			Updates(map[string]interface{}{
				"status":            dto.RefundStatusRefunded,
				"refunded_at":       refundedAt,
				"platform_trans_id": platformTransID,
			}).Error; err != nil {
			return err
		}

		var refund model.RefundAudit
		if err := tx.Where("id = ?", refundID).First(&refund).Error; err != nil {
			return err
		}

		if err := tx.Model(&model.BillRecord{}).
			Where("id = ?", refund.BillID).
			Updates(map[string]interface{}{
				"refund_status":   dto.RefundStatusRefunded,
				"refund_amount":   refund.RefundAmount,
				"refund_order_no": refund.RefundOrderNo,
				"status":          dto.BillStatusRefunded,
			}).Error; err != nil {
			return err
		}

		return nil
	})
}

func (m *BillManager) UpdateRoundSettlementDeductSuccess(ctx context.Context, roundTraceID string, successCount int, deductedAt time.Time) error {
	return m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ?", roundTraceID).
		Updates(map[string]interface{}{
			"deduct_success_count": successCount,
			"deducted_at":          deductedAt,
		}).Error
}

func (m *BillManager) UpdateRoundSettlementSettleInfo(ctx context.Context, roundTraceID string, senderID int64, senderType string, totalAmount, commission int64, playerCount int, minPlayerID int64) error {
	return m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ?", roundTraceID).
		Updates(map[string]interface{}{
			"sender_id":     senderID,
			"sender_type":   senderType,
			"total_amount":  totalAmount,
			"commission":    commission,
			"player_count":  playerCount,
			"min_player_id": minPlayerID,
		}).Error
}

func (m *BillManager) GetBillByBatchAndUser(ctx context.Context, batchID string, userID int64) (*model.BillRecord, error) {
	var bill model.BillRecord
	err := m.db.WithContext(ctx).Where("batch_id = ? AND user_id = ?", batchID, userID).First(&bill).Error
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (m *BillManager) GetRetryableCredits(ctx context.Context, limit int) ([]*model.BillRecord, error) {
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

func (m *BillManager) SetNextRetryTime(ctx context.Context, billID int64, nextRetryAt time.Time) error {
	return m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Update("next_retry_at", nextRetryAt).Error
}

func (m *BillManager) IncrementRetryCountWithNextRetryTime(ctx context.Context, billID int64, nextRetryAt time.Time) error {
	return m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Updates(map[string]interface{}{
			"retry_count":   gorm.Expr("retry_count + 1"),
			"next_retry_at": nextRetryAt,
		}).Error
}

func (m *BillManager) GetFailedFirstRoundSettlements(ctx context.Context, since time.Time, limit int) ([]*model.RoundSettlement, error) {
	var settlements []*model.RoundSettlement
	err := m.db.WithContext(ctx).Where("deduct_scene = ? AND status = ? AND created_at < ?",
		dto.DeductSceneFirstRoundShare, dto.RoundStatusFailed, since).
		Order("created_at asc").
		Limit(limit).
		Find(&settlements).Error
	return settlements, err
}

func (m *BillManager) GetDeductedButNotSettled(ctx context.Context, since time.Time, limit int) ([]*model.RoundSettlement, error) {
	var settlements []*model.RoundSettlement
	err := m.db.WithContext(ctx).Where("status = ? AND settle_amount IS NULL AND created_at < ?",
		dto.RoundStatusDeducted, since).
		Order("created_at asc").
		Limit(limit).
		Find(&settlements).Error
	return settlements, err
}

func (m *BillManager) UpdateBillExceptionID(ctx context.Context, billID, exceptionID int64) error {
	return m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("id = ?", billID).
		Update("exception_id", exceptionID).Error
}

func (m *BillManager) GetRefundsByStatus(ctx context.Context, status int, limit int, offset int) ([]*model.RefundAudit, error) {
	var refunds []*model.RefundAudit
	err := m.db.WithContext(ctx).Model(&model.RefundAudit{}).
		Where("status = ?", status).
		Order("applied_at asc").
		Limit(limit).
		Offset(offset).
		Find(&refunds).Error
	return refunds, err
}

// AggregateBetBySession 按玩家聚合该游戏的扣款金额（amount < 0 的 Bill），返回 map[userID]abs(sum(amount))
func (m *BillManager) AggregateBetBySession(ctx context.Context, sessionID int64) (map[int64]int64, error) {
	type result struct {
		UserID     int64
		TotalAmount int64
	}
	var results []result
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Select("user_id, SUM(ABS(amount)) as total_amount").
		Where("session_id = ? AND amount < 0 AND status = ? AND user_id != ?",
			sessionID, dto.BillStatusSuccess, dto.PlatformAccountID).
		Group("user_id").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	resultMap := make(map[int64]int64, len(results))
	for _, r := range results {
		resultMap[r.UserID] = r.TotalAmount
	}
	return resultMap, nil
}

// GetBillsBySessionTypeAndUser 查询指定会话、类型、用户的账单（用于幂等检查）
func (m *BillManager) GetBillsBySessionTypeAndUser(ctx context.Context, sessionID int64, billType int, userID int64) ([]*model.BillRecord, error) {
	var bills []*model.BillRecord
	err := m.db.WithContext(ctx).Where("session_id = ? AND bill_type = ? AND user_id = ?", sessionID, billType, userID).Find(&bills).Error
	return bills, err
}

// CreateBillsOnly 仅批量创建账单，不更新轮次结算状态
// 用于 creditRound：round_settlement.status 由 SettleRound 在所有子结算（credit+reward）成功后统一置为 Credited
func (m *BillManager) CreateBillsOnly(ctx context.Context, bills []*model.BillRecord) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, bill := range bills {
			if err := tx.Create(bill).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// AggregatePayOutBySession 按玩家聚合该游戏的入账金额（amount > 0 的 Bill），返回 map[userID]sum(amount)
func (m *BillManager) AggregatePayOutBySession(ctx context.Context, sessionID int64) (map[int64]int64, error) {
	type result struct {
		UserID     int64
		TotalAmount int64
	}
	var results []result
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Select("user_id, SUM(amount) as total_amount").
		Where("session_id = ? AND amount > 0 AND status = ? AND user_id != ? AND bill_type != ?",
			sessionID, dto.BillStatusSuccess, dto.PlatformAccountID, dto.BillTypeSessionCredit).
		Group("user_id").
		Find(&results).Error
	if err != nil {
		return nil, err
	}
	resultMap := make(map[int64]int64, len(results))
	for _, r := range results {
		resultMap[r.UserID] = r.TotalAmount
	}
	return resultMap, nil
}

// UpdateGameSettleStatusByUser 标记该玩家在该游戏中所有 Bill 的游戏级结算状态
func (m *BillManager) UpdateGameSettleStatusByUser(ctx context.Context, sessionID int64, userID int64, status int) error {
	now := time.Now()
	updates := map[string]interface{}{
		"game_settle_status": status,
	}
	if status == dto.BillGameSettleSettled {
		updates["game_settled_at"] = now
	}
	return m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		Updates(updates).Error
}

// GetUnsettledUsersBySession 查询游戏中未完成游戏级结算的玩家列表
func (m *BillManager) GetUnsettledUsersBySession(ctx context.Context, sessionID int64) ([]int64, error) {
	var userIDs []int64
	err := m.db.WithContext(ctx).Model(&model.BillRecord{}).
		Select("DISTINCT user_id").
		Where("session_id = ? AND status = ? AND game_settle_status = ? AND user_id != ?",
			sessionID, dto.BillStatusSuccess, dto.BillGameSettleNone, dto.PlatformAccountID).
		Find(&userIDs).Error
	return userIDs, err
}

// GetAllRoundSettlementsBySession 查询该游戏所有回合的结算记录
func (m *BillManager) GetAllRoundSettlementsBySession(ctx context.Context, sessionID int64) ([]*model.RoundSettlement, error) {
	var settlements []*model.RoundSettlement
	err := m.db.WithContext(ctx).Where("session_id = ?", sessionID).
		Order("round_no asc").
		Find(&settlements).Error
	return settlements, err
}

// UpdateGameSettleStatusBySession 标记该游戏所有 RoundSettlement 的游戏级结算状态
func (m *BillManager) UpdateGameSettleStatusBySession(ctx context.Context, sessionID int64, status int) error {
	updates := map[string]interface{}{
		"game_settle_status": status,
	}
	if status == dto.GameSettleStatusSuccess {
		now := time.Now()
		updates["game_settled_at"] = now
	}
	return m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("session_id = ?", sessionID).
		Updates(updates).Error
}

// GetFailedGameSettlements 查询游戏级结算失败的 Session（用于重试）
func (m *BillManager) GetFailedGameSettlements(ctx context.Context, limit int) ([]int64, error) {
	var sessionIDs []int64
	err := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Select("DISTINCT session_id").
		Where("game_settle_status = ?", dto.GameSettleStatusFailed).
		Limit(limit).
		Find(&sessionIDs).Error
	return sessionIDs, err
}

// GetTimedOutGameSettlements 查询超时未结算的游戏（所有回合 Credited 但 GameSettleStatus 仍为 None，且超过指定时间）
func (m *BillManager) GetTimedOutGameSettlements(ctx context.Context, timeout time.Duration, limit int) ([]int64, error) {
	cutoff := time.Now().Add(-timeout)
	var sessionIDs []int64
	err := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Select("DISTINCT session_id").
		Where("status = ? AND game_settle_status = ? AND updated_at < ?",
			dto.RoundStatusCredited, dto.GameSettleStatusNone, cutoff).
		Limit(limit).
		Find(&sessionIDs).Error
	return sessionIDs, err
}
