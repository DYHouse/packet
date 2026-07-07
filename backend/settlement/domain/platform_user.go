package domain

// PlatformUser 表示平台用户的核心信息，用于 settlement 领域与 game 领域解耦。
// 字段与 game 层 User 模型中被 settlement 实际使用的字段保持一致（仅 UserID）。
type PlatformUser struct {
	// UserID 为平台侧用户标识，对应 game 层 User 模型的 UserID 字段。
	UserID string
}
