package mysql

import (
	"context"
	"time"

	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

// gormSnapshotRepository 轮次玩家快照仓储实现
type gormSnapshotRepository struct {
	db *gorm.DB
}

// NewGormSnapshotRepository 创建快照仓储实例
func NewGormSnapshotRepository(db *gorm.DB) *gormSnapshotRepository {
	return &gormSnapshotRepository{db: db}
}

// BatchCreateOnRoundStart round 开始时批量插入玩家快照
func (r *gormSnapshotRepository) BatchCreateOnRoundStart(ctx context.Context, snapshots []*model.RoundPlayerSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(snapshots, 100).Error
}

// MarkPlayerLeft 标记玩家在某轮离开（被踢/离座/替补）
// 乐观锁：WHERE active_end IS NULL 避免重复标记
func (r *gormSnapshotRepository) MarkPlayerLeft(ctx context.Context, sessionID, roundID, userID int64, leftAt time.Time, reason string) error {
	result := r.db.WithContext(ctx).Model(&model.RoundPlayerSnapshot{}).
		Where("session_id = ? AND round_id = ? AND user_id = ? AND active_end IS NULL",
			sessionID, roundID, userID).
		Updates(map[string]interface{}{
			"active_end":  leftAt,
			"left_reason": reason,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// 已被标记或不存在，幂等无副作用
		return nil
	}
	return nil
}

// AddPlayerMidRound 中途加入（替补/重新入座/成为旁观者）
func (r *gormSnapshotRepository) AddPlayerMidRound(ctx context.Context, snapshot *model.RoundPlayerSnapshot) error {
	return r.db.WithContext(ctx).Create(snapshot).Error
}

// ListByRound 查询某轮的所有玩家快照
func (r *gormSnapshotRepository) ListByRound(ctx context.Context, sessionID, roundID int64) ([]*model.RoundPlayerSnapshot, error) {
	var snapshots []*model.RoundPlayerSnapshot
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND round_id = ?", sessionID, roundID).
		Order("seat_no ASC, active_start ASC").
		Find(&snapshots).Error
	return snapshots, err
}

// ListByUser 查询某玩家在某会话的所有参与轮次
func (r *gormSnapshotRepository) ListByUser(ctx context.Context, sessionID, userID int64) ([]*model.RoundPlayerSnapshot, error) {
	var snapshots []*model.RoundPlayerSnapshot
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		Order("round_no ASC, active_start ASC").
		Find(&snapshots).Error
	return snapshots, err
}
