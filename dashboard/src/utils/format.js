export const formatMoney = (value) => {
  if (!value) return '¥0.00'
  return '¥' + (value / 100).toLocaleString('zh-CN', {
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

// toYuan 将分转为元的数值, 用于 ECharts 等需要原始数值的场景
export const toYuan = (value) => {
  if (!value) return 0
  return value / 100
}

export const formatHour = (hourStr) => {
  return hourStr + ':00'
}

export const formatDate = (dateStr) => {
  return dateStr.substring(5, 10)
}
