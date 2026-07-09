package domain

import "context"

// VirtualBalanceService 虚拟余额服务接口
// 收敛所有虚拟余额 Redis 操作到 settlement 层，消除 game 与 settlement 双写隐式耦合。
// 接口方法签名与原 game 层 VirtualBalanceService 完全一致，确保行为不变。
type VirtualBalanceService interface {
	// Deduct 虚拟扣款（Lua 原子操作）
	// 通过 luaDeductBalance 脚本原子执行 "扣减 → 余额检查 → 回滚 → 标记 dirty"，
	// 消除原 Go 代码三步之间的竞态。
	Deduct(ctx context.Context, userID int64, amount int64) error
	// Credit 虚拟入账（原子操作）
	Credit(ctx context.Context, userID int64, amount int64) error
	// GetBalance 查询虚拟余额
	GetBalance(ctx context.Context, userID int64) (int64, error)
	// SyncToDB 批量同步脏数据到DB（由定时任务调用）
	SyncToDB(ctx context.Context) error
	// AddToRobotSet 添加到机器人ID集合（供RobotChecker使用）
	AddToRobotSet(ctx context.Context, userID int64) error
	// SetBalance 设置虚拟余额（初始化时使用）
	SetBalance(ctx context.Context, userID int64, balance int64) error
}

// RobotAccountStore 机器人账户存储接口
// 用于解耦 settlement 与 game/infrastructure/persistence/mysql，
// 仅暴露 VirtualBalanceRepository 实际需要的两个方法。
// game 层在 bootstrap 阶段提供适配器实现该接口。
type RobotAccountStore interface {
	// GetVirtualBalance 根据 userID 查询机器人账户的虚拟余额
	GetVirtualBalance(ctx context.Context, userID int64) (int64, error)
	// UpdateBalance 更新机器人账户的虚拟余额
	UpdateBalance(ctx context.Context, userID int64, balance int64) error
}
