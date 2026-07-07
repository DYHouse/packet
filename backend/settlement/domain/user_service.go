package domain

import "context"

// UserService 提供用户信息查询能力，由 game 层实现并注入到 settlement 层。
// 接口方法签名与 settlement 原有 service.UserService 保持一致，仅返回类型
// 由 game 层 User 模型替换为 *PlatformUser，从而解除对 game 模型层的反向依赖。
type UserService interface {
	// GetUserById 根据 cashparty 内部 ID（字符串形式）查询用户信息。
	GetUserById(ctx context.Context, id string) (*PlatformUser, error)
}
