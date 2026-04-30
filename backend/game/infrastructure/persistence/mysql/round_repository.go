package mysql

import (
	"context"

	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

type gormRoundRepository struct {
	db *gorm.DB
}

func NewGormRoundRepository(db *gorm.DB) domain.RoundDBRepository {
	return &gormRoundRepository{db: db}
}

func (r *gormRoundRepository) CreateRound(ctx context.Context, round *model.Round) error {
	return r.db.WithContext(ctx).Create(round).Error
}

func (r *gormRoundRepository) GetRound(ctx context.Context, roundID int64) (*model.Round, error) {
	var round model.Round
	err := r.db.WithContext(ctx).First(&round, roundID).Error
	if err != nil {
		return nil, err
	}
	return &round, nil
}

func (r *gormRoundRepository) GetRoundBySessionAndNo(ctx context.Context, sessionID int64, roundNo int) (*model.Round, error) {
	var round model.Round
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND round_no = ?", sessionID, roundNo).
		First(&round).Error
	if err != nil {
		return nil, err
	}
	return &round, nil
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
