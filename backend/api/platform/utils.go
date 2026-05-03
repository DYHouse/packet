package platform

import "github.com/cashparty/backend/common/currency"

// ParseAmount 解析元字符串为分（委托到 currency 包）。
var ParseAmount = currency.ParseAmount

// FormatAmount 格式化分为元字符串（委托到 currency 包）。
var FormatAmount = currency.FormatAmount
