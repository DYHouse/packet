package scripts

import (
	cRedis "github.com/cashparty/backend/common/redis"
)

var (
	// 虚拟余额（2）
	DeductBalance = cRedis.NewScript("deduct_balance", luaDeductBalance)
	CreditBalance = cRedis.NewScript("credit_balance", luaCreditBalance)
)
