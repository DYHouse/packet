// formatMoney 将元值格式化为 "¥xx.xx" 显示字符串
// 值已经是元（后端 currency.Money 自动转换），无需再除 100
export const formatMoney = (value) => {
  if (!value && value !== 0) return '¥0.00'
  const num = typeof value === 'string' ? parseFloat(value) : value
  return '¥' + num.toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  })
}

export const formatNumber = (value) => {
  if (!value) return '0'
  return value.toLocaleString('zh-CN')
}

export const formatPercent = (value) => {
  if (!value) return '0%'
  return (value * 100).toFixed(2) + '%'
}

// toYuan 值已经是元，直接返回数值（保留函数以兼容调用方）
export const toYuan = (value) => {
  if (!value && value !== 0) return 0
  return typeof value === 'string' ? parseFloat(value) : value
}

export const formatHour = (hourStr) => {
  return hourStr + ':00'
}

export const formatDate = (dateStr) => {
  return dateStr.substring(5, 10)
}
