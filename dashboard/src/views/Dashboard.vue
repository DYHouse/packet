<template>
  <div class="dashboard">
    <header class="dashboard-header">
      <div class="header-left">
        <h1>红包游戏数据看板</h1>
        <span class="update-time">更新时间: {{ updateTime }}</span>
      </div>
      <div class="header-right">
        <DatePicker 
          v-model:start-date="startDate" 
          v-model:end-date="endDate"
        />
        <button class="refresh-btn" @click="refreshData" :disabled="loading">
          {{ loading ? '刷新中...' : '刷新数据' }}
        </button>
      </div>
    </header>

    <div v-if="errorMessage" class="error-banner">
      {{ errorMessage }}
    </div>

    <main class="dashboard-content">
      <section class="section">
        <div class="section-header">
          <h2>{{ dateLabel }}概览</h2>
        </div>
        <div class="stats-grid">
          <StatCard :value="dashboard.today_rounds" label="局数" />
          <StatCard :value="dashboard.active_players" label="活跃玩家" />
          <StatCard :value="dashboard.total_commission" label="总抽佣" type="money" />
          <StatCard :value="dashboard.penalty_income" label="罚金收入" type="money" />
          <StatCard :value="dashboard.net_profit" label="平台收益" type="money" />
          <StatCard :value="dashboard.system_packet_cost" label="系统红包支出" type="money" />
          <StatCard :value="dashboard.straight_count" label="顺子触发" />
          <StatCard :value="dashboard.leopard_count" label="豹子触发" />
          <StatCard :value="dashboard.system_packet_count" label="系统红包数" />
        </div>
      </section>

      <section class="section">
        <div class="section-header">
          <h2>小时趋势</h2>
        </div>
        <div class="charts-grid-2">
          <div class="chart-section">
            <LineChart
              :data="hourlyTrend"
              :title="hourlyTrendTitle"
              x-field="hour"
              :y-fields="['round_count', 'commission']"
              :y-names="['局数', '抽佣(元)']"
            />
          </div>
          <div class="chart-section">
            <BarChart
              :data="amountDistribution"
              :title="amountDistributionTitle"
              x-field="range"
              :y-fields="['count']"
              :y-names="['红包数量']"
            />
          </div>
        </div>
      </section>

      <section class="section">
        <div class="section-header">
          <h2>数据分析</h2>
        </div>
        <div class="charts-grid-2">
          <div class="chart-section">
            <LineChart
              :data="dailyTrend"
              :title="dailyTrendTitle"
              x-field="date"
              :y-fields="['net_profit', 'total_commission']"
              :y-names="['净收益(元)', '总抽佣(元)']"
            />
          </div>
          <div class="chart-section">
            <RankTable
              :data="roomRanking"
              :title="roomRankingTitle"
            />
          </div>
        </div>
      </section>

      <section class="section">
        <div class="section-header">
          <h2>系统红包统计</h2>
        </div>
        <div class="system-packets-grid">
          <div v-for="item in systemPacketStats" :key="item.sender_type" class="packet-type-card">
            <div class="packet-type-label">{{ senderTypeMap[item.sender_type] || item.sender_type }}</div>
            <div class="packet-type-stats">
              <div class="packet-stat">
                <span class="packet-stat-value">{{ item.send_count }}</span>
                <span class="packet-stat-unit">次</span>
              </div>
              <div class="packet-stat">
                <span class="packet-stat-value">{{ formatMoney(item.total_amount) }}</span>
                <span class="packet-stat-unit">总金额</span>
              </div>
              <div class="packet-stat">
                <span class="packet-stat-value">{{ formatMoney(item.avg_amount) }}</span>
                <span class="packet-stat-unit">均值</span>
              </div>
            </div>
          </div>
          <div v-if="systemPacketStats.length === 0 && !sectionLoading.systemPackets" class="packet-type-card empty">
            暂无系统红包数据
          </div>
        </div>
      </section>
    </main>
  </div>
</template>

<script setup>
import { ref, onMounted, computed, watch } from 'vue'
import StatCard from '../components/cards/StatCard.vue'
import LineChart from '../components/charts/LineChart.vue'
import BarChart from '../components/charts/BarChart.vue'
import RankTable from '../components/cards/RankTable.vue'
import DatePicker from '../components/DatePicker.vue'
import { statsApi } from '../api/stats'
import { formatMoney, formatHour, formatDate, toYuan } from '../utils/format'

const loading = ref(false)
const updateTime = ref('')
const errorMessage = ref('')

const formatDateStr = (date) => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const today = formatDateStr(new Date())
const startDate = ref(today)
const endDate = ref(today)

const dashboard = ref({
  today_sessions: 0,
  today_rounds: 0,
  active_players: 0,
  total_commission: 0,
  penalty_income: 0,
  system_packet_cost: 0,
  net_profit: 0,
  straight_count: 0,
  leopard_count: 0,
  system_packet_count: 0
})

const hourlyTrend = ref([])
const dailyTrend = ref([])
const amountDistribution = ref([])
const roomRanking = ref([])
const systemPacketStats = ref([])

const sectionLoading = ref({
  dashboard: false,
  hourlyTrend: false,
  dailyTrend: false,
  amountDistribution: false,
  roomRanking: false,
  systemPackets: false,
})

const senderTypeMap = {
  system: '系统红包',
  system_forced: '强制红包',
  system_resume: '续场红包',
}

const dateLabel = computed(() => {
  if (startDate.value === endDate.value) {
    return startDate.value === today ? '今日' : startDate.value
  }
  return `${startDate.value} ~ ${endDate.value}`
})

const hourlyTrendTitle = computed(() => {
  if (startDate.value === endDate.value) {
    return `${startDate.value} 小时趋势`
  }
  return `${startDate.value} ~ ${endDate.value} 小时趋势`
})

const amountDistributionTitle = computed(() => {
  if (startDate.value === endDate.value) {
    return `${startDate.value} 红包金额分布`
  }
  return `${startDate.value} ~ ${endDate.value} 红包金额分布`
})

const dailyTrendTitle = computed(() => {
  return `${startDate.value} ~ ${endDate.value} 收益趋势`
})

const roomRankingTitle = computed(() => {
  if (startDate.value === endDate.value) {
    return `${startDate.value} 房间热度排行 TOP 10`
  }
  return `${startDate.value} ~ ${endDate.value} 房间热度排行 TOP 10`
})

const updateTimeStr = () => {
  const now = new Date()
  updateTime.value = now.toLocaleString('zh-CN')
}

const fetchDashboard = async () => {
  sectionLoading.value.dashboard = true
  try {
    const data = await statsApi.getDashboard(startDate.value, endDate.value)
    dashboard.value = data
  } finally {
    sectionLoading.value.dashboard = false
  }
}

const fetchHourlyTrend = async () => {
  sectionLoading.value.hourlyTrend = true
  try {
    const data = await statsApi.getHourlyTrend(startDate.value, endDate.value)
    hourlyTrend.value = data.map(item => ({
      ...item,
      hour: formatHour(item.hour),
      commission: toYuan(item.commission)
    }))
  } finally {
    sectionLoading.value.hourlyTrend = false
  }
}

const fetchDailyTrend = async () => {
  sectionLoading.value.dailyTrend = true
  try {
    const data = await statsApi.getDailyTrend(startDate.value, endDate.value)
    dailyTrend.value = data.map(item => ({
      ...item,
      date: formatDate(item.date),
      net_profit: toYuan(item.net_profit),
      total_commission: toYuan(item.total_commission),
      system_packet_cost: toYuan(item.system_packet_cost),
      penalty_income: toYuan(item.penalty_income)
    }))
  } finally {
    sectionLoading.value.dailyTrend = false
  }
}

const fetchAmountDistribution = async () => {
  sectionLoading.value.amountDistribution = true
  try {
    amountDistribution.value = await statsApi.getAmountDistribution(startDate.value, endDate.value)
  } finally {
    sectionLoading.value.amountDistribution = false
  }
}

const fetchRoomRanking = async () => {
  sectionLoading.value.roomRanking = true
  try {
    roomRanking.value = await statsApi.getRoomRanking(startDate.value, endDate.value)
  } finally {
    sectionLoading.value.roomRanking = false
  }
}

const fetchSystemPacketStats = async () => {
  sectionLoading.value.systemPackets = true
  try {
    systemPacketStats.value = await statsApi.getSystemPacketStats(startDate.value, endDate.value)
  } finally {
    sectionLoading.value.systemPackets = false
  }
}

const refreshData = async () => {
  if (loading.value) return
  
  loading.value = true
  errorMessage.value = ''
  try {
    await Promise.all([
      fetchDashboard(),
      fetchHourlyTrend(),
      fetchDailyTrend(),
      fetchAmountDistribution(),
      fetchRoomRanking(),
      fetchSystemPacketStats(),
    ])
    updateTimeStr()
  } catch (error) {
    errorMessage.value = '数据加载失败，请稍后重试'
  } finally {
    loading.value = false
  }
}

watch([startDate, endDate], () => {
  refreshData()
})

onMounted(() => {
  refreshData()
})
</script>

<style scoped>
.dashboard {
  min-height: 100vh;
  background: var(--color-bg);
}

.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 24px;
  background: var(--color-bg-section);
  border-bottom: 1px solid var(--color-border);
}

.header-left {
  display: flex;
  align-items: baseline;
  gap: 16px;
}

.dashboard-header h1 {
  font-size: 18px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.update-time {
  font-size: 13px;
  color: var(--color-text-tertiary);
}

.header-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.refresh-btn {
  padding: 6px 16px;
  background: var(--color-bg-section);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  color: var(--color-text-primary);
  font-size: 13px;
  cursor: pointer;
  transition: background-color 0.2s;
}

.refresh-btn:hover {
  background: var(--color-border-light);
}

.refresh-btn:disabled {
  color: var(--color-text-tertiary);
  cursor: not-allowed;
}

.error-banner {
  padding: 10px 24px;
  background: #fff3f3;
  color: var(--color-danger);
  font-size: 13px;
  border-bottom: 1px solid #fde2e2;
}

.dashboard-content {
  padding: 24px;
}

.section {
  margin-bottom: 32px;
}

.section:last-child {
  margin-bottom: 0;
}

.section-header {
  margin-bottom: 16px;
}

.section-header h2 {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 16px;
}

.charts-grid-2 {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 24px;
}

.chart-section {
  background: var(--color-bg-section);
  padding: 20px;
}

.system-packets-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 16px;
}

.packet-type-card {
  background: var(--color-bg-section);
  padding: 20px;
}

.packet-type-card.empty {
  color: var(--color-text-tertiary);
  text-align: center;
  padding: 32px 20px;
  font-size: 13px;
}

.packet-type-label {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: 12px;
}

.packet-type-stats {
  display: flex;
  gap: 20px;
}

.packet-stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.packet-stat-value {
  font-size: 16px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.packet-stat-unit {
  font-size: 12px;
  color: var(--color-text-tertiary);
}

@media (max-width: 1400px) {
  .stats-grid {
    grid-template-columns: repeat(3, 1fr);
  }
}

@media (max-width: 1000px) {
  .stats-grid {
    grid-template-columns: repeat(2, 1fr);
  }
  
  .charts-grid-2 {
    grid-template-columns: 1fr;
  }

  .system-packets-grid {
    grid-template-columns: 1fr;
  }
}
</style>
