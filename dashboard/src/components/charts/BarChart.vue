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
    default: 'range'
  },
  yFields: {
    type: Array,
    default: () => ['count']
  },
  yNames: {
    type: Array,
    default: () => ['数量']
  }
})

const chartRef = ref(null)
let chartInstance = null

const colors = ['#1a1a1a', '#666666', '#999999']

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
    type: 'bar',
    data: props.data.map(item => item[field]),
    itemStyle: {
      color: colors[index % colors.length]
    },
    barWidth: props.yFields.length > 1 ? '30%' : '50%'
  }))

  const option = {
    title: {
      text: props.title,
      textStyle: {
        color: '#1a1a1a',
        fontSize: 15,
        fontWeight: 600
      },
      left: 0,
      top: 0
    },
    tooltip: {
      trigger: 'axis',
      backgroundColor: '#ffffff',
      borderColor: '#e5e5e5',
      borderWidth: 1,
      textStyle: {
        color: '#1a1a1a'
      }
    },
    legend: props.yFields.length > 1 ? {
      data: props.yNames,
      textStyle: { color: '#666666' },
      top: 0,
      right: 0
    } : undefined,
    grid: {
      left: 50,
      right: 20,
      top: 50,
      bottom: 40
    },
    xAxis: {
      type: 'category',
      data: xAxisData,
      axisLine: {
        lineStyle: {
          color: '#e5e5e5'
        }
      },
      axisLabel: {
        color: '#666666',
        fontSize: 12,
        rotate: 30
      },
      axisTick: {
        show: false
      }
    },
    yAxis: {
      type: 'value',
      axisLine: {
        show: false
      },
      axisLabel: {
        color: '#666666',
        fontSize: 12
      },
      splitLine: {
        lineStyle: {
          color: '#f0f0f0'
        }
      },
      axisTick: {
        show: false
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
