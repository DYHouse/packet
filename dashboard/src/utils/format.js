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

export const formatHour = (hourStr) => {
  return hourStr.substring(11, 16)
}

export const formatDate = (dateStr) => {
  return dateStr.substring(5, 10)
}
