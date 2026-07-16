package mysql

import (
	"context"
	"fmt"
	"time"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gormRoundRepository struct {
	db *gorm.DB
}

func NewGormRoundRepository(db *gorm.DB) repository.RoundDBRepository {
	return &gormRoundRepository{db: db}
}

func (r *gormRoundRepository) CreateRound(ctx context.Context, round *model.Round) error {
	return r.db.WithContext(ctx).Create(round).Error
}

// CreateOrGetRound 按 (session_id, round_no) 幂等创建 round。
// 采用 INSERT IGNORE 模式（与 user_repository.go 的 OnConflict{DoNothing:true} 一致）：
// - 正常路径：INSERT 成功，RowsAffected=1，返回传入的 round
// - 冲突路径：INSERT IGNORE 忽略冲突，RowsAffected=0，查询已存在记录返回
// 该模式为 DB 层原子操作，无 TOCTOU 窗口，无需启用 GORM TranslateError。
func (r *gormRoundRepository) CreateOrGetRound(ctx context.Context, round *model.Round) (*model.Round, error) {
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}, {Name: "round_no"}},
		DoNothing: true,
	}).Create(round)

	if result.Error != nil {
		return nil, result.Error
	}

	// RowsAffected=0 表示冲突被忽略，查询已存在的 round
	if result.RowsAffected == 0 {
		existing, err := r.GetRoundBySessionAndRoundNo(ctx, round.SessionID, round.RoundNo)
		if err != nil {
			return nil, fmt.Errorf("conflict ignored but query failed: %w", err)
		}
		return existing, nil
	}

	return round, nil
}

func (r *gormRoundRepository) UpdateRoundStatus(ctx context.Context, roundID int64, status model.RoundStatus) error {
	return r.db.WithContext(ctx).Model(&model.Round{}).
		Where("round_id = ?", roundID).
		Update("status", status).Error
}

func (r *gormRoundRepository) UpdateRoundDeductInfo(ctx context.Context, roundID int64, deductScene, deductStatus int, deductAmount int64, batchID string) error {
	return r.db.WithContext(ctx).Model(&model.Round{}).
		Where("round_id = ?", roundID).
		Updates(map[string]interface{}{
			"deduct_scene":  deductScene,
			"deduct_status": deductStatus,
			"deduct_amount": deductAmount,
			"batch_id":      batchID,
		}).Error
}

func (r *gormRoundRepository) UpdateRoundFailed(ctx context.Context, roundID int64, reason string) error {
	return r.db.WithContext(ctx).Model(&model.Round{}).
		Where("round_id = ?", roundID).
		Updates(map[string]interface{}{
			"status":        model.RoundStatusFailed,
			"failed_reason": reason,
		}).Error
}

func (r *gormRoundRepository) UpdateRoundSender(ctx context.Context, roundID int64, senderID int64, senderType string) error {
	return r.db.WithContext(ctx).Model(&model.Round{}).
		Where("round_id = ?", roundID).
		Updates(map[string]interface{}{
			"sender_id":   senderID,
			"sender_type": senderType,
		}).Error
}

func (r *gormRoundRepository) UpdateRoundAmount(ctx context.Context, roundID int64, totalAmount, commission int64) error {
	return r.db.WithContext(ctx).Model(&model.Round{}).
		Where("round_id = ?", roundID).
		Updates(map[string]interface{}{
			"total_amount": totalAmount,
			"commission":   commission,
		}).Error
}

func (r *gormRoundRepository) GetRoundByRoundID(ctx context.Context, roundID int64) (*model.Round, error) {
	var round model.Round
	if err := r.db.WithContext(ctx).Where("round_id = ?", roundID).First(&round).Error; err != nil {
		return nil, err
	}
	return &round, nil
}

func (r *gormRoundRepository) GetRoundBySessionAndRoundNo(ctx context.Context, sessionID int64, roundNo int) (*model.Round, error) {
	var round model.Round
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND round_no = ?", sessionID, roundNo).
		First(&round).Error
	if err != nil {
		return nil, err
	}
	return &round, nil
}

func (r *gormRoundRepository) UpdateRoundSending(ctx context.Context, roundID, senderID int64, senderType string, startedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Round{}).
		Where("round_id = ?", roundID).
		Updates(map[string]interface{}{
			"status":     model.RoundStatusSending,
			"sender_id":  senderID,
			"started_at": &startedAt,
		}).Error
}

func (r *gormRoundRepository) UpdateRoundEnded(ctx context.Context, roundID, settleTraceID int64, endedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Round{}).
		Where("round_id = ?", roundID).
		Updates(map[string]interface{}{
			"status":          model.RoundStatusEnded,
			"ended_at":        &endedAt,
			"settle_trace_id": settleTraceID,
		}).Error
}
