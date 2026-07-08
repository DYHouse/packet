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

// roundSettlementRepository 实现 domain.RoundSettlementRepository 接口，负责 RoundSettlement 的 CRUD 与状态机更新。
// 代码由 BillManager 迁移而来，逻辑保持一致。
type roundSettlementRepository struct {
	db *gorm.DB
}

// NewRoundSettlementRepository 创建 RoundSettlementRepository 实例，返回接口类型。
func NewRoundSettlementRepository(db *gorm.DB) domain.RoundSettlementRepository {
	return &roundSettlementRepository{db: db}
}

// 编译期断言：确保 roundSettlementRepository 实现 domain.RoundSettlementRepository 接口。
var _ domain.RoundSettlementRepository = (*roundSettlementRepository)(nil)

func (m *roundSettlementRepository) ExistsRoundSettlement(ctx context.Context, roundID int64) (bool, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_id = ?", roundID).
		Count(&count).Error
	return count > 0, err
}

func (m *roundSettlementRepository) UpdateRoundSettlementCredited(ctx context.Context, traceID string, settleAmount int64, settleUserCount int, settledAt *time.Time) error {
	// 乐观锁：只允许从非 Credited 状态转换到 Credited，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ? AND status != ?", traceID, dto.RoundStatusCredited).
		Updates(map[string]interface{}{
			"status":               dto.RoundStatusCredited,
			"settle_amount":        settleAmount,
			"settle_user_count":    settleUserCount,
			"settle_success_count": settleUserCount,
			"settled_at":           settledAt,
		})
	if result.Error != nil {
		return fmt.Errorf("update round settlement credited failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已被其他事务标记为 Credited，视为幂等成功
		return nil
	}
	return nil
}

func (m *roundSettlementRepository) UpdateRoundSettlementStatus(ctx context.Context, traceID string, status int, errMsg string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	// 乐观锁：若已是终态 Credited，拒绝覆盖（状态机只能向前推进）。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ? AND status != ?", traceID, dto.RoundStatusCredited).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update round settlement status failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已是 Credited 终态，视为幂等成功
		return nil
	}
	return nil
}

func (m *roundSettlementRepository) GetRoundSettlementByRoundID(ctx context.Context, roundID int64) (*model.RoundSettlement, error) {
	var settlement model.RoundSettlement
	err := m.db.WithContext(ctx).Where("round_id = ?", roundID).First(&settlement).Error
	if err != nil {
		return nil, err
	}
	return &settlement, nil
}

// CreateRoundSettlementAndBills 创建回合结算与配对账单。事务边界由 AppService 通过
// DBRepository.WithTransaction 编排：在事务回调内通过 tx.RoundSettlementRepo() 获取的子 repo，
// 其 m.db 即为事务连接，settlement 与 bills 的多次 Create 自动纳入同一事务。
func (m *roundSettlementRepository) CreateRoundSettlementAndBills(ctx context.Context, settlement *model.RoundSettlement, bills []*model.BillRecord) error {
	if err := m.db.WithContext(ctx).Create(settlement).Error; err != nil {
		return fmt.Errorf("create round settlement failed: %w", err)
	}
	for _, bill := range bills {
		if err := m.db.WithContext(ctx).Create(bill).Error; err != nil {
			return fmt.Errorf("create bill failed: %w", err)
		}
	}
	return nil
}

func (m *roundSettlementRepository) UpdateRoundSettlementDeductSuccess(ctx context.Context, roundTraceID string, successCount int, deductedAt time.Time) error {
	// 乐观锁：只允许在 deducted_at 未设置时更新，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ? AND deducted_at IS NULL", roundTraceID).
		Updates(map[string]interface{}{
			"deduct_success_count": successCount,
			"deducted_at":          deductedAt,
		})
	if result.Error != nil {
		return fmt.Errorf("update round settlement deduct success failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已记录扣款成功（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

func (m *roundSettlementRepository) UpdateRoundSettlementSettleInfo(ctx context.Context, roundTraceID string, senderID int64, senderType string, totalAmount, commission int64, playerCount int, minPlayerID int64) error {
	// 乐观锁：只允许在 settle_info 未填充（sender_id = 0）时更新，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("round_trace_id = ? AND sender_id = 0", roundTraceID).
		Updates(map[string]interface{}{
			"sender_id":     senderID,
			"sender_type":   senderType,
			"total_amount":  totalAmount,
			"commission":    commission,
			"player_count":  playerCount,
			"min_player_id": minPlayerID,
		})
	if result.Error != nil {
		return fmt.Errorf("update round settlement settle info failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已填充 settle_info（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

func (m *roundSettlementRepository) GetFailedFirstRoundSettlements(ctx context.Context, since time.Time, limit int) ([]*model.RoundSettlement, error) {
	var settlements []*model.RoundSettlement
	err := m.db.WithContext(ctx).Where("deduct_scene = ? AND status = ? AND created_at < ?",
		dto.DeductSceneFirstRoundShare, dto.RoundStatusFailed, since).
		Order("created_at asc").
		Limit(limit).
		Find(&settlements).Error
	return settlements, err
}

func (m *roundSettlementRepository) GetDeductedButNotSettled(ctx context.Context, since time.Time, limit int) ([]*model.RoundSettlement, error) {
	var settlements []*model.RoundSettlement
	err := m.db.WithContext(ctx).Where("status = ? AND settle_amount IS NULL AND created_at < ?",
		dto.RoundStatusDeducted, since).
		Order("created_at asc").
		Limit(limit).
		Find(&settlements).Error
	return settlements, err
}

// GetAllRoundSettlementsBySession 查询该游戏所有回合的结算记录
func (m *roundSettlementRepository) GetAllRoundSettlementsBySession(ctx context.Context, sessionID int64) ([]*model.RoundSettlement, error) {
	var settlements []*model.RoundSettlement
	err := m.db.WithContext(ctx).Where("session_id = ?", sessionID).
		Order("round_no asc").
		Find(&settlements).Error
	return settlements, err
}

// UpdateGameSettleStatusBySession 标记该游戏所有 RoundSettlement 的游戏级结算状态
func (m *roundSettlementRepository) UpdateGameSettleStatusBySession(ctx context.Context, sessionID int64, fromStatus, toStatus int) error {
	updates := map[string]interface{}{
		"game_settle_status": toStatus,
	}
	if toStatus == dto.GameSettleStatusSuccess {
		now := time.Now()
		updates["game_settled_at"] = now
	}
	// 乐观锁：只允许从 fromStatus 转换，防止并发覆盖。
	// 调用方应检查 RowsAffected == 0 表示已被其他事务处理。
	result := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Where("session_id = ? AND game_settle_status = ?", sessionID, fromStatus).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update game settle status by session failed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已不是 fromStatus（已被其他事务处理），视为幂等成功
		return nil
	}
	return nil
}

// GetFailedGameSettlements 查询游戏级结算失败的 Session（用于重试）
func (m *roundSettlementRepository) GetFailedGameSettlements(ctx context.Context, limit int) ([]int64, error) {
	var sessionIDs []int64
	err := m.db.WithContext(ctx).Model(&model.RoundSettlement{}).
		Select("DISTINCT session_id").
		Where("game_settle_status = ?", dto.GameSettleStatusFailed).
		Limit(limit).
		Find(&sessionIDs).Error
	return sessionIDs, err
}

// GetTimedOutGameSettlements 查询超时未结算的游戏（所有回合 Credited 但 GameSettleStatus 仍为 None，且超过指定时间）
func (m *roundSettlementRepository) GetTimedOutGameSettlements(ctx context.Context, timeout time.Duration, limit int) ([]int64, error) {
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
