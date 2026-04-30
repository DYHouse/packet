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
    grid: {
      left: 50,
      right: 20,
      top: 50,
      bottom: 40
    },
    xAxis: {
      type: 'category',
      data: props.data.map(item => item.range),
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
    series: [
      {
        name: '红包数量',
        type: 'bar',
        data: props.data.map(item => item.count),
        itemStyle: {
          color: '#666666'
        },
        barWidth: '50%'
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
