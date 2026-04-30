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

  getRoomRanking(startDate, endDate, limit = 10) {
    const params = startDate && endDate 
      ? `?start_date=${startDate}&end_date=${endDate}&limit=${limit}` 
      : `?limit=${limit}`
    return request.get(`/api/v1/stats/rooms/ranking${params}`)
  },

  getSystemPacketStats(date) {
    const params = date ? `?date=${date}` : ''
    return request.get(`/api/v1/stats/system-packets${params}`)
  }
}
