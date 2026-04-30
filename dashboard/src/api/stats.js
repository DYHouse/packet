import request from '../utils/request'

export const statsApi = {
  getDashboard(startDate, endDate) {
    const params = startDate && endDate 
      ? `?start_date=${startDate}&end_date=${endDate}` 
      : ''
    return request.get(`/api/v1/stats/dashboard${params}`)
  },

  getHourlyTrend(startDate, endDate) {
    const params = startDate && endDate 
      ? `?start_date=${startDate}&end_date=${endDate}` 
      : ''
    return request.get(`/api/v1/stats/trend/hourly${params}`)
  },

  getDailyTrend(startDate, endDate) {
    const params = startDate && endDate 
      ? `?start_date=${startDate}&end_date=${endDate}` 
      : ''
    return request.get(`/api/v1/stats/trend/daily${params}`)
  },

  getAmountDistribution(startDate, endDate) {
    const params = startDate && endDate 
      ? `?start_date=${startDate}&end_date=${endDate}` 
      : ''
    return request.get(`/api/v1/stats/distribution${params}`)
  },

  getRoomRanking(startDate, endDate, limit = 10, offset = 0) {
    const params = startDate && endDate 
      ? `?start_date=${startDate}&end_date=${endDate}&limit=${limit}&offset=${offset}` 
      : `?limit=${limit}&offset=${offset}`
    return request.get(`/api/v1/stats/rooms/ranking${params}`)
  },

  // 改用 start_date+end_date 参数, 与其他接口统一
  getSystemPacketStats(startDate, endDate) {
    const params = startDate && endDate 
      ? `?start_date=${startDate}&end_date=${endDate}` 
      : ''
    return request.get(`/api/v1/stats/system-packets${params}`)
  }
}
