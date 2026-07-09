package adapter

import (
	"github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/settlement/domain"
)

// FeeCalculatorAdapter 实现 settlement/domain.FeeCalculator 接口，
// 内部委托 game/domain/room.CalculateRequiredFee，作为 settlement 模块与 game 模块之间的解耦适配器。
type FeeCalculatorAdapter struct{}

// NewFeeCalculatorAdapter 构造 FeeCalculatorAdapter 实例。
func NewFeeCalculatorAdapter() *FeeCalculatorAdapter {
	return &FeeCalculatorAdapter{}
}

// CalculateRequiredFee 计算开局所需总费用，委托 room.CalculateRequiredFee。
// 透传逻辑与原 room.CalculateRequiredFee 完全一致，业务零变更。
func (a *FeeCalculatorAdapter) CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64 {
	return room.CalculateRequiredFee(roomFee, maxPlayers, maxRounds)
}

// 编译期断言：*FeeCalculatorAdapter 实现 settlement/domain.FeeCalculator 接口。
var _ domain.FeeCalculator = (*FeeCalculatorAdapter)(nil)
