package reward

// 奖励类型常量（1顺子, 2豹子）
const (
	RewardTypeStraight = 1
	RewardTypeLeopard  = 2
)

// 触发类型常量（1保底, 2概率）
const (
	TriggerTypeGuarantee   = 1
	TriggerTypeProbability = 2
)

// 奖励倍数常量（游戏领域单一数据源）
// 顺子奖励倍数：1.0（即奖励金额 = 本金）
// 豹子奖励倍数：10.0（即奖励金额 = 本金 × 10）
const (
	StraightRewardMultiplier float64 = 1.0
	LeopardRewardMultiplier  float64 = 10.0
)

// 游戏结果常量（用于平台上报）
const (
	GameResultWin  = "win"
	GameResultLose = "lose"
)

// CalculateRewardAmount 根据奖励类型和本金计算奖励金额（游戏领域规则）。
// 顺子（type=1）：奖励 = totalAmount × 1.0
// 豹子（type=2）：奖励 = totalAmount × 10.0
// 其他类型返回 0。
func CalculateRewardAmount(rewardType int, totalAmount int64) int64 {
	switch rewardType {
	case RewardTypeStraight:
		return int64(float64(totalAmount) * StraightRewardMultiplier)
	case RewardTypeLeopard:
		return int64(float64(totalAmount) * LeopardRewardMultiplier)
	default:
		return 0
	}
}

// DetermineGameResult 根据派奖金额和下注金额判定游戏输赢（游戏领域规则）。
// 派奖 > 下注 → win，否则 → lose。
func DetermineGameResult(payOut, betAmount int64) string {
	if payOut > betAmount {
		return GameResultWin
	}
	return GameResultLose
}
