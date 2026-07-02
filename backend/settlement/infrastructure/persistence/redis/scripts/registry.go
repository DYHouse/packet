package scripts

import (
	cRedis "github.com/cashparty/backend/common/redis"
)

var (
	// 虚拟余额（1）
	DeductBalance = cRedis.NewScript("deduct_balance", luaDeductBalance)
)
