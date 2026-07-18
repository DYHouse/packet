package application

// calculateSubstituteFee 计算替补费金额 = 房费 * 佣金率（5%）。
// 佣金率与 game.CommissionConfig.DefaultCommissionConfig 保持一致（0.05），
// 如 5000 房费 → 250 替补费。返回 0 表示免扣（RoomFee 为 0 时跳过扣款）。
// 注：settlement 层不持有 CommissionConfig，此处直接使用固定费率计算，
// 避免在调用方新增 commissionCfg 依赖。
// 供 RoomAppService.tryAutoSubstitute 和 SeatAppService.SetReady 复用，
// 保证两条替补路径（排队队列替补 / 观众手动补位）扣款金额一致。
func calculateSubstituteFee(roomFee int64) int64 {
	if roomFee <= 0 {
		return 0
	}
	return int64(float64(roomFee) * 0.05)
}
