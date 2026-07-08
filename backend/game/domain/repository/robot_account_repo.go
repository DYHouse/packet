package repository

import (
	"context"

	"github.com/cashparty/backend/game/model"
)

// RobotAccountRepository 机器人账户仓储接口
// 抽象 RobotAccountService 对 MySQL 的依赖，让 application 层不再直接依赖 infrastructure。
type RobotAccountRepository interface {
	Create(ctx context.Context, account *model.RobotAccount) error
	GetByUserID(ctx context.Context, userID int64) (*model.RobotAccount, error)
	UpdateStatus(ctx context.Context, userID int64, status int) error
	UpdateBalance(ctx context.Context, userID int64, balance int64) error
	GetAvailableRobots(ctx context.Context, minRoomFee, maxRoomFee int) ([]*model.RobotAccount, error)
	Count(ctx context.Context) (int64, error)
	UpdateLastActiveAt(ctx context.Context, userID int64) error
}
