package adapter

import (
	"context"
	"fmt"

	gameModel "github.com/cashparty/backend/game/model"
	"github.com/cashparty/backend/settlement/domain"
)

// userProvider 定义 adapter 所依赖的用户查询能力。
// game 层的 *application.UserService 实现了该接口（含 Redis 缓存逻辑），
// 通过接口约束避免对 application 包的硬编码依赖。
type userProvider interface {
	// GetUserById 根据 cashparty 内部 ID（字符串形式）查询用户，返回 *gameModel.User。
	GetUserById(ctx context.Context, id string) (*gameModel.User, error)
}

// UserSaverAdapter 将 game 层的 User 模型适配为 settlement 层的 PlatformUser，
// 实现 settlement/domain.UserService 接口。adapter 位于 game 层，因此可合法依赖
// game/model；settlement 层仅依赖 domain.UserService 接口，不再反向依赖 game/model。
type UserSaverAdapter struct {
	userSvc userProvider
}

// NewUserSaverAdapter 创建适配器。userSvc 通常为 *application.UserService。
func NewUserSaverAdapter(userSvc userProvider) *UserSaverAdapter {
	return &UserSaverAdapter{userSvc: userSvc}
}

// GetUserById 实现 settlement/domain.UserService 接口。
// 委托给底层 userProvider 查询 gameModel.User，再映射为 domain.PlatformUser。
func (a *UserSaverAdapter) GetUserById(ctx context.Context, id string) (*domain.PlatformUser, error) {
	user, err := a.userSvc.GetUserById(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get user by id failed: %w", err)
	}
	return &domain.PlatformUser{
		UserID: user.UserID,
	}, nil
}

// 编译期断言：*UserSaverAdapter 实现 settlement/domain.UserService 接口。
var _ domain.UserService = (*UserSaverAdapter)(nil)
