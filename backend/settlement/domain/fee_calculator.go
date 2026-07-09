package domain

// FeeCalculator 计算开局所需总费用（解耦 settlement 对 game/domain/room 的依赖）。
// 实现方在 game/infrastructure/adapter 层提供，内部委托 game/domain/room.CalculateRequiredFee。
type FeeCalculator interface {
	// CalculateRequiredFee 根据房费、最大玩家数和最大局数计算开局所需总费用。
	// 方法签名与 game/domain/room.CalculateRequiredFee 完全一致，确保业务零变更。
	CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64
}
