package mysql

import (
	"context"
	"time"

	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	"gorm.io/gorm"
)

// robotAccountRepository 机器人账户数据仓库
type robotAccountRepository struct {
	db *gorm.DB
}

// NewRobotAccountRepository 创建机器人账户仓库实例
func NewRobotAccountRepository(db *gorm.DB) repository.RobotAccountRepository {
	return &robotAccountRepository{db: db}
}

// Create 创建机器人账户
func (r *robotAccountRepository) Create(ctx context.Context, account *model.RobotAccount) error {
	return r.db.WithContext(ctx).Create(account).Error
}

// GetByUserID 根据 UserID 查询机器人账户
func (r *robotAccountRepository) GetByUserID(ctx context.Context, userID int64) (*model.RobotAccount, error) {
	var account model.RobotAccount
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&account).Error
	if err != nil {
		return nil, err
	}
	return &account, nil
}

// UpdateStatus 更新机器人状态
func (r *robotAccountRepository) UpdateStatus(ctx context.Context, userID int64, status int) error {
	return r.db.WithContext(ctx).Model(&model.RobotAccount{}).
		Where("user_id = ?", userID).
		Update("status", status).Error
}

// UpdateBalance 更新虚拟余额
func (r *robotAccountRepository) UpdateBalance(ctx context.Context, userID int64, balance int64) error {
	return r.db.WithContext(ctx).Model(&model.RobotAccount{}).
		Where("user_id = ?", userID).
		Update("virtual_balance", balance).Error
}

// GetAvailableRobots 获取可用的机器人列表
// 过滤条件: MinRoomFee <= maxRoomFee AND MaxRoomFee >= minRoomFee AND Status = 1
// 按 VirtualBalance 升序排序
func (r *robotAccountRepository) GetAvailableRobots(ctx context.Context, minRoomFee, maxRoomFee int) ([]*model.RobotAccount, error) {
	var accounts []*model.RobotAccount
	err := r.db.WithContext(ctx).
		Where("min_room_fee <= ? AND max_room_fee >= ? AND status = ?", maxRoomFee, minRoomFee, model.RobotStatusIdle).
		Order("virtual_balance ASC").
		Find(&accounts).Error
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// Count 统计总机器人数量
func (r *robotAccountRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.RobotAccount{}).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// UpdateLastActiveAt 更新最后活跃时间
func (r *robotAccountRepository) UpdateLastActiveAt(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Model(&model.RobotAccount{}).
		Where("user_id = ?", userID).
		Update("last_active_at", time.Now()).Error
}
