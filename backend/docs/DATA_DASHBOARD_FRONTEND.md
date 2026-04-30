# 数据大屏前端开发指南

> 基于 Vue3 + ECharts 的数据可视化大屏实现

---

## 一、技术选型

| 技术 | 版本 | 用途 |
|-----|------|------|
| Vue 3 | 3.x | 前端框架 |
| Vite | 5.x | 构建工具 |
| ECharts | 5.x | 图表库 |
| Axios | 1.x | HTTP 请求 |
| Pinia | 2.x | 状态管理（可选） |
| Element Plus | 2.x | UI 组件库（可选） |

---

## 二、项目结构

```
dashboard/
├── public/
│   └── favicon.ico
├── src/
│   ├── api/
│   │   └── stats.js              # 统计数据 API
│   ├── components/
│   │   ├── charts/
│   │   │   ├── LineChart.vue     # 折线图组件
│   │   │   ├── BarChart.vue      # 柱状图组件
│   │   │   └── PieChart.vue      # 饼图组件
│   │   ├── cards/
│   │   │   ├── StatCard.vue      # 统计卡片
│   │   │   └── RankTable.vue     # 排行榜表格
│   │   └── layout/
│   │       └── DashboardLayout.vue
│   ├── views/
│   │   └── Dashboard.vue         # 大屏主页面
│   ├── utils/
│   │   ├── request.js            # Axios 封装
│   │   └── format.js             # 格式化工具
│   ├── styles/
│   │   ├── index.css             # 全局样式
│   │   └── dashboard.css         # 大屏样式
│   ├── App.vue
│   └── main.js
├── index.html
├── package.json
└── vite.config.js
```

---

## 三、项目初始化

### 3.1 创建项目

```bash
# 使用 Vite 创建项目
npm create vite@latest dashboard -- --template vue

# 进入项目目录
cd dashboard

# 安装依赖
npm install echarts axios
```

### 3.2 package.json

```json
{
  "name": "dashboard",
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "vue": "^3.4.0",
    "echarts": "^5.5.0",
    "axios": "^1.6.0"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "^5.0.0",
    "vite": "^5.0.0"
  }
}
```

### 3.3 vite.config.js

```javascript
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true
      }
    }
  }
})
```

---

## 四、核心代码实现

### 4.1 API 封装 (src/api/stats.js)

```javascript
import request from '../utils/request'

export const statsApi = {
  getDashboard() {
    return request.get('/api/v1/stats/dashboard')
  },

  getHourlyTrend(hours = 24) {
    return request.get(`/api/v1/stats/trend/hourly?hours=${hours}`)
  },

  getDailyTrend(days = 7) {
    return request.get(`/api/v1/stats/trend/daily?days=${days}`)
  },

  getAmountDistribution() {
    return request.get('/api/v1/stats/distribution')
  },

  getRoomRanking(limit = 10) {
    return request.get(`/api/v1/stats/rooms/ranking?limit=${limit}`)
  },

  getSystemPacketStats() {
    return request.get('/api/v1/stats/system-packets')
  }
}
```

### 4.2 Axios 封装 (src/utils/request.js)

```javascript
import axios from 'axios'

const request = axios.create({
  baseURL: '',
  timeout: 10000
})

request.interceptors.response.use(
  response => {
    const res = response.data
    if (res.code === 0) {
      return res.data
    }
    return Promise.reject(new Error(res.message || 'Error'))
  },
  error => {
    return Promise.reject(error)
  }
)

export default request
```

### 4.3 格式化工具 (src/utils/format.js)

```javascript
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
```

### 4.4 统计卡片组件

```vue
<template>
  <div class="stat-card">
    <div class="stat-icon" :style="{ backgroundColor: color }">
      <span>{{ icon }}</span>
    </div>
    <div class="stat-content">
      <div class="stat-value">{{ formattedValue }}</div>
      <div class="stat-label">{{ label }}</div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatMoney, formatNumber } from '../utils/format'

const props = defineProps({
  value: {
    type: [Number, String],
    default: 0
  },
  label: {
    type: String,
    required: true
  },
  icon: {
    type: String,
    default: '📊'
  },
  color: {
    type: String,
    default: '#409EFF'
  },
  type: {
    type: String,
    default: 'number'
  }
})

const formattedValue = computed(() => {
  if (props.type === 'money') {
    return formatMoney(props.value)
  }
  return formatNumber(props.value)
})
</script>

<style scoped>
.stat-card {
  display: flex;
  align-items: center;
  padding: 20px;
  background: linear-gradient(135deg, #1a1f36 0%, #252b48 100%);
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.1);
}

.stat-icon {
  width: 60px;
  height: 60px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28px;
  margin-right: 16px;
}

.stat-content {
  flex: 1;
}

.stat-value {
  font-size: 28px;
  font-weight: bold;
  color: #fff;
  margin-bottom: 4px;
}

.stat-label {
  font-size: 14px;
  color: rgba(255, 255, 255, 0.6);
}
</style>
```

### 4.5 折线图组件

```vue
<template>
  <div ref="chartRef" class="chart-container"></div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import * as echarts from 'echarts'

const props = defineProps({
  data: {
    type: Array,
    default: () => []
  },
  title: {
    type: String,
    default: ''
  },
  xField: {
    type: String,
    default: 'hour'
  },
  yFields: {
    type: Array,
    default: () => ['round_count']
  },
  yNames: {
    type: Array,
    default: () => ['局数']
  },
  colors: {
    type: Array,
    default: () => ['#409EFF', '#67C23A', '#E6A23C']
  }
})

const chartRef = ref(null)
let chartInstance = null

const initChart = () => {
  if (!chartRef.value) return
  
  chartInstance = echarts.init(chartRef.value)
  updateChart()
}

const updateChart = () => {
  if (!chartInstance || !props.data.length) return

  const xAxisData = props.data.map(item => item[props.xField])
  const series = props.yFields.map((field, index) => ({
    name: props.yNames[index] || field,
    type: 'line',
    smooth: true,
    data: props.data.map(item => item[field]),
    itemStyle: {
      color: props.colors[index]
    },
    areaStyle: {
      color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
        { offset: 0, color: props.colors[index] + '40' },
        { offset: 1, color: props.colors[index] + '00' }
      ])
    }
  }))

  const option = {
    title: {
      text: props.title,
      textStyle: {
        color: '#fff',
        fontSize: 16
      },
      left: 20,
      top: 10
    },
    tooltip: {
      trigger: 'axis',
      backgroundColor: 'rgba(0, 0, 0, 0.8)',
      borderColor: 'rgba(255, 255, 255, 0.2)',
      textStyle: {
        color: '#fff'
      }
    },
    legend: {
      data: props.yNames,
      textStyle: {
        color: 'rgba(255, 255, 255, 0.7)'
      },
      top: 10,
      right: 20
    },
    grid: {
      left: 60,
      right: 20,
      top: 60,
      bottom: 40
    },
    xAxis: {
      type: 'category',
      data: xAxisData,
      axisLine: {
        lineStyle: {
          color: 'rgba(255, 255, 255, 0.2)'
        }
      },
      axisLabel: {
        color: 'rgba(255, 255, 255, 0.6)',
        fontSize: 12
      }
    },
    yAxis: {
      type: 'value',
      axisLine: {
        lineStyle: {
          color: 'rgba(255, 255, 255, 0.2)'
        }
      },
      axisLabel: {
        color: 'rgba(255, 255, 255, 0.6)',
        fontSize: 12
      },
      splitLine: {
        lineStyle: {
          color: 'rgba(255, 255, 255, 0.1)'
        }
      }
    },
    series
  }

  chartInstance.setOption(option)
}

const handleResize = () => {
  chartInstance?.resize()
}

watch(() => props.data, () => {
  updateChart()
}, { deep: true })

onMounted(() => {
  initChart()
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  chartInstance?.dispose()
})
</script>

<style scoped>
.chart-container {
  width: 100%;
  height: 300px;
}
</style>
```

### 4.6 柱状图组件

```vue
<template>
  <div ref="chartRef" class="chart-container"></div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import * as echarts from 'echarts'

const props = defineProps({
  data: {
    type: Array,
    default: () => []
  },
  title: {
    type: String,
    default: ''
  }
})

const chartRef = ref(null)
let chartInstance = null

const initChart = () => {
  if (!chartRef.value) return
  
  chartInstance = echarts.init(chartRef.value)
  updateChart()
}

const updateChart = () => {
  if (!chartInstance || !props.data.length) return

  const option = {
    title: {
      text: props.title,
      textStyle: {
        color: '#fff',
        fontSize: 16
      },
      left: 20,
      top: 10
    },
    tooltip: {
      trigger: 'axis',
      backgroundColor: 'rgba(0, 0, 0, 0.8)',
      borderColor: 'rgba(255, 255, 255, 0.2)',
      textStyle: {
        color: '#fff'
      }
    },
    grid: {
      left: 60,
      right: 20,
      top: 60,
      bottom: 40
    },
    xAxis: {
      type: 'category',
      data: props.data.map(item => item.range),
      axisLine: {
        lineStyle: {
          color: 'rgba(255, 255, 255, 0.2)'
        }
      },
      axisLabel: {
        color: 'rgba(255, 255, 255, 0.6)',
        fontSize: 12,
        rotate: 30
      }
    },
    yAxis: {
      type: 'value',
      axisLine: {
        lineStyle: {
          color: 'rgba(255, 255, 255, 0.2)'
        }
      },
      axisLabel: {
        color: 'rgba(255, 255, 255, 0.6)',
        fontSize: 12
      },
      splitLine: {
        lineStyle: {
          color: 'rgba(255, 255, 255, 0.1)'
        }
      }
    },
    series: [
      {
        name: '红包数量',
        type: 'bar',
        data: props.data.map(item => item.count),
        itemStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: '#409EFF' },
            { offset: 1, color: '#409EFF40' }
          ])
        },
        barWidth: '40%'
      }
    ]
  }

  chartInstance.setOption(option)
}

const handleResize = () => {
  chartInstance?.resize()
}

watch(() => props.data, () => {
  updateChart()
}, { deep: true })

onMounted(() => {
  initChart()
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  chartInstance?.dispose()
})
</script>

<style scoped>
.chart-container {
  width: 100%;
  height: 300px;
}
</style>
```

### 4.7 排行榜组件

```vue
<template>
  <div class="rank-table">
    <div class="table-header">
      <span class="title">{{ title }}</span>
    </div>
    <div class="table-body">
      <div class="table-row header-row">
        <div class="col col-rank">排名</div>
        <div class="col col-name">房间名称</div>
        <div class="col">局数</div>
        <div class="col">玩家</div>
        <div class="col">流水</div>
        <div class="col">顺子</div>
        <div class="col">豹子</div>
      </div>
      <div 
        v-for="(item, index) in data" 
        :key="item.room_id"
        class="table-row"
        :class="{ 'top-three': index < 3 }"
      >
        <div class="col col-rank">
          <span class="rank-badge" :class="'rank-' + (index + 1)">
            {{ index + 1 }}
          </span>
        </div>
        <div class="col col-name">{{ item.room_name || '未命名' }}</div>
        <div class="col">{{ item.round_count }}</div>
        <div class="col">{{ item.player_count }}</div>
        <div class="col">{{ formatMoney(item.total_amount) }}</div>
        <div class="col">{{ item.straight_count }}</div>
        <div class="col">{{ item.leopard_count }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { formatMoney } from '../utils/format'

defineProps({
  data: {
    type: Array,
    default: () => []
  },
  title: {
    type: String,
    default: '房间排行'
  }
})
</script>

<style scoped>
.rank-table {
  background: linear-gradient(135deg, #1a1f36 0%, #252b48 100%);
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  overflow: hidden;
}

.table-header {
  padding: 16px 20px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
}

.table-header .title {
  font-size: 16px;
  font-weight: bold;
  color: #fff;
}

.table-body {
  max-height: 400px;
  overflow-y: auto;
}

.table-row {
  display: flex;
  align-items: center;
  padding: 12px 20px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
}

.table-row.header-row {
  background: rgba(255, 255, 255, 0.05);
  font-weight: bold;
  color: rgba(255, 255, 255, 0.8);
}

.table-row:not(.header-row):hover {
  background: rgba(255, 255, 255, 0.05);
}

.table-row.top-three {
  background: rgba(64, 158, 255, 0.1);
}

.col {
  flex: 1;
  text-align: center;
  color: rgba(255, 255, 255, 0.8);
  font-size: 14px;
}

.col-rank {
  flex: 0.5;
}

.col-name {
  flex: 1.5;
  text-align: left;
}

.rank-badge {
  display: inline-block;
  width: 24px;
  height: 24px;
  line-height: 24px;
  text-align: center;
  border-radius: 50%;
  font-size: 12px;
  font-weight: bold;
}

.rank-badge.rank-1 {
  background: linear-gradient(135deg, #FFD700, #FFA500);
  color: #fff;
}

.rank-badge.rank-2 {
  background: linear-gradient(135deg, #C0C0C0, #A0A0A0);
  color: #fff;
}

.rank-badge.rank-3 {
  background: linear-gradient(135deg, #CD7F32, #B8860B);
  color: #fff;
}
</style>
```

### 4.8 大屏主页面

```vue
<template>
  <div class="dashboard">
    <header class="dashboard-header">
      <h1>🎮 红包游戏数据大屏</h1>
      <div class="header-right">
        <span class="update-time">更新时间: {{ updateTime }}</span>
        <button class="refresh-btn" @click="refreshData" :disabled="loading">
          {{ loading ? '刷新中...' : '刷新数据' }}
        </button>
      </div>
    </header>

    <main class="dashboard-content">
      <section class="stats-cards">
        <StatCard
          :value="dashboard.today_rounds"
          label="今日局数"
          icon="🎯"
          color="#409EFF"
        />
        <StatCard
          :value="dashboard.active_players"
          label="活跃玩家"
          icon="👥"
          color="#67C23A"
        />
        <StatCard
          :value="dashboard.net_profit"
          label="平台收益"
          icon="💰"
          color="#E6A23C"
          type="money"
        />
        <StatCard
          :value="dashboard.straight_count"
          label="顺子触发"
          icon="🎲"
          color="#F56C6C"
        />
        <StatCard
          :value="dashboard.leopard_count"
          label="豹子触发"
          icon="🃏"
          color="#909399"
        />
      </section>

      <section class="charts-row">
        <div class="chart-panel">
          <LineChart
            :data="hourlyTrend"
            title="24小时游戏趋势"
            x-field="hour"
            :y-fields="['round_count', 'commission']"
            :y-names="['局数', '抽佣(分)']"
            :colors="['#409EFF', '#67C23A']"
          />
        </div>
        <div class="chart-panel">
          <BarChart
            :data="amountDistribution"
            title="红包金额分布"
          />
        </div>
      </section>

      <section class="charts-row">
        <div class="chart-panel">
          <LineChart
            :data="dailyTrend"
            title="近7日收益趋势"
            x-field="date"
            :y-fields="['net_profit', 'total_commission']"
            :y-names="['净收益(分)', '总抽佣(分)']"
            :colors="['#E6A23C', '#409EFF']"
          />
        </div>
        <div class="chart-panel">
          <RankTable
            :data="roomRanking"
            title="房间热度排行 TOP 10"
          />
        </div>
      </section>

      <section class="system-packets">
        <div class="panel-header">
          <h3>🎁 平台红包统计</h3>
        </div>
        <div class="packet-stats">
          <div class="packet-item" v-for="item in systemPackets" :key="item.sender_type">
            <div class="packet-type">{{ getSenderTypeName(item.sender_type) }}</div>
            <div class="packet-info">
              <span class="count">{{ item.send_count }} 次</span>
              <span class="amount">{{ formatMoney(item.total_amount) }}</span>
            </div>
          </div>
        </div>
      </section>
    </main>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import StatCard from '../components/cards/StatCard.vue'
import LineChart from '../components/charts/LineChart.vue'
import BarChart from '../components/charts/BarChart.vue'
import RankTable from '../components/cards/RankTable.vue'
import { statsApi } from '../api/stats'
import { formatMoney, formatHour, formatDate } from '../utils/format'

const loading = ref(false)
const updateTime = ref('')

const dashboard = ref({
  today_sessions: 0,
  today_rounds: 0,
  active_players: 0,
  total_commission: 0,
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
const systemPackets = ref([])

const getSenderTypeName = (type) => {
  const names = {
    'system': '系统发红包',
    'system_forced': '强制发红包',
    'system_resume': '恢复发红包'
  }
  return names[type] || type
}

const updateTimeStr = () => {
  const now = new Date()
  updateTime.value = now.toLocaleString('zh-CN')
}

const fetchDashboard = async () => {
  const data = await statsApi.getDashboard()
  dashboard.value = data
}

const fetchHourlyTrend = async () => {
  const data = await statsApi.getHourlyTrend(24)
  hourlyTrend.value = data.map(item => ({
    ...item,
    hour: formatHour(item.hour)
  }))
}

const fetchDailyTrend = async () => {
  const data = await statsApi.getDailyTrend(7)
  dailyTrend.value = data.map(item => ({
    ...item,
    date: formatDate(item.date)
  }))
}

const fetchAmountDistribution = async () => {
  amountDistribution.value = await statsApi.getAmountDistribution()
}

const fetchRoomRanking = async () => {
  roomRanking.value = await statsApi.getRoomRanking(10)
}

const fetchSystemPackets = async () => {
  systemPackets.value = await statsApi.getSystemPacketStats()
}

const refreshData = async () => {
  if (loading.value) return
  
  loading.value = true
  try {
    await Promise.all([
      fetchDashboard(),
      fetchHourlyTrend(),
      fetchDailyTrend(),
      fetchAmountDistribution(),
      fetchRoomRanking(),
      fetchSystemPackets()
    ])
    updateTimeStr()
  } catch (error) {
    console.error('刷新数据失败:', error)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  refreshData()
})
</script>

<style scoped>
.dashboard {
  min-height: 100vh;
  background: linear-gradient(135deg, #0f1219 0%, #1a1f36 50%, #0f1219 100%);
  color: #fff;
}

.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 20px 40px;
  background: rgba(0, 0, 0, 0.3);
  border-bottom: 1px solid rgba(255, 255, 255, 0.1);
}

.dashboard-header h1 {
  font-size: 28px;
  font-weight: bold;
  background: linear-gradient(90deg, #409EFF, #67C23A);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 20px;
}

.update-time {
  font-size: 14px;
  color: rgba(255, 255, 255, 0.6);
}

.refresh-btn {
  padding: 8px 20px;
  background: linear-gradient(135deg, #409EFF, #67C23A);
  border: none;
  border-radius: 20px;
  color: #fff;
  font-size: 14px;
  cursor: pointer;
  transition: all 0.3s;
}

.refresh-btn:hover {
  transform: scale(1.05);
}

.refresh-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.dashboard-content {
  padding: 20px 40px;
}

.stats-cards {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 20px;
  margin-bottom: 20px;
}

.charts-row {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 20px;
  margin-bottom: 20px;
}

.chart-panel {
  background: linear-gradient(135deg, #1a1f36 0%, #252b48 100%);
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  padding: 20px;
}

.system-packets {
  background: linear-gradient(135deg, #1a1f36 0%, #252b48 100%);
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  padding: 20px;
}

.panel-header {
  margin-bottom: 20px;
}

.panel-header h3 {
  font-size: 16px;
  font-weight: bold;
  color: #fff;
}

.packet-stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
}

.packet-item {
  background: rgba(255, 255, 255, 0.05);
  border-radius: 8px;
  padding: 16px;
  text-align: center;
}

.packet-type {
  font-size: 14px;
  color: rgba(255, 255, 255, 0.6);
  margin-bottom: 8px;
}

.packet-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.packet-info .count {
  font-size: 24px;
  font-weight: bold;
  color: #409EFF;
}

.packet-info .amount {
  font-size: 14px;
  color: rgba(255, 255, 255, 0.8);
}

@media (max-width: 1400px) {
  .stats-cards {
    grid-template-columns: repeat(3, 1fr);
  }
}

@media (max-width: 1000px) {
  .stats-cards {
    grid-template-columns: repeat(2, 1fr);
  }
  
  .charts-row {
    grid-template-columns: 1fr;
  }
  
  .packet-stats {
    grid-template-columns: 1fr;
  }
}
</style>
```

### 4.9 入口文件

```vue
<template>
  <Dashboard />
</template>

<script setup>
import Dashboard from './views/Dashboard.vue'
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: 'PingFang SC', 'Microsoft YaHei', sans-serif;
  -webkit-font-smoothing: antialiased;
}
</style>
```

### 4.10 main.js

```javascript
import { createApp } from 'vue'
import App from './App.vue'

createApp(App).mount('#app')
```

---

## 五、全局样式 (src/styles/index.css)

```css
:root {
  --primary-color: #409EFF;
  --success-color: #67C23A;
  --warning-color: #E6A23C;
  --danger-color: #F56C6C;
  --info-color: #909399;
  --bg-dark: #0f1219;
  --bg-card: #1a1f36;
}

body {
  background-color: var(--bg-dark);
  color: #fff;
}

::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}

::-webkit-scrollbar-track {
  background: rgba(255, 255, 255, 0.1);
  border-radius: 3px;
}

::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.3);
  border-radius: 3px;
}

::-webkit-scrollbar-thumb:hover {
  background: rgba(255, 255, 255, 0.5);
}
```

---

## 六、运行与部署

### 6.1 开发环境

```bash
# 安装依赖
npm install

# 启动开发服务器
npm run dev
```

### 6.2 生产构建

```bash
# 构建生产版本
npm run build

# 预览生产版本
npm run preview
```

### 6.3 Nginx 配置

```nginx
server {
    listen 80;
    server_name dashboard.example.com;

    root /var/www/dashboard/dist;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    location /api {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

---

## 七、扩展功能

### 7.1 自动刷新（可选）

在 `Dashboard.vue` 中添加自动刷新：

```javascript
import { onMounted, onUnmounted } from 'vue'

let refreshTimer = null

onMounted(() => {
  refreshData()
  // 每5分钟自动刷新
  refreshTimer = setInterval(refreshData, 5 * 60 * 1000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
  }
})
```

### 7.2 日期选择器（可选）

添加日期范围选择功能：

```vue
<template>
  <div class="date-picker">
    <input type="date" v-model="startDate" />
    <span>至</span>
    <input type="date" v-model="endDate" />
    <button @click="queryByDate">查询</button>
  </div>
</template>
```

### 7.3 导出报表（可选）

添加 Excel 导出功能：

```bash
npm install xlsx
```

```javascript
import * as XLSX from 'xlsx'

const exportToExcel = (data, filename) => {
  const ws = XLSX.utils.json_to_sheet(data)
  const wb = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(wb, ws, 'Sheet1')
  XLSX.writeFile(wb, `${filename}.xlsx`)
}
```

---

## 八、效果预览

大屏页面包含以下模块：

1. **顶部标题栏** - 显示标题、更新时间、刷新按钮
2. **核心指标卡片** - 今日局数、活跃玩家、平台收益、顺子/豹子触发次数
3. **24小时趋势图** - 折线图展示局数和抽佣趋势
4. **红包金额分布** - 柱状图展示不同金额区间的红包数量
5. **7日收益趋势** - 折线图展示每日净收益和总抽佣
6. **房间排行榜** - 表格展示 TOP 10 房间数据
7. **平台红包统计** - 展示不同类型的系统发红包数据

---

## 九、注意事项

1. **跨域问题** - 开发环境通过 Vite proxy 解决，生产环境通过 Nginx 配置
2. **数据格式** - 金额单位为分，前端需要除以100显示
3. **图表自适应** - 监听窗口 resize 事件，自动调整图表大小
4. **错误处理** - 添加 API 请求失败的提示
5. **性能优化** - 大数据量时考虑分页或虚拟滚动
