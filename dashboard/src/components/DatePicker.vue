<template>
  <div class="date-picker">
    <div class="quick-buttons">
      <button 
        v-for="option in quickOptions" 
        :key="option.value"
        :class="['quick-btn', { active: selectedQuick === option.value }]"
        @click="selectQuick(option.value)"
      >
        {{ option.label }}
      </button>
    </div>
    <div class="date-range">
      <input 
        type="date" 
        :value="startDate"
        @change="onStartDateChange"
        class="date-input"
      />
      <span class="date-separator">~</span>
      <input 
        type="date" 
        :value="endDate"
        @change="onEndDateChange"
        class="date-input"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, watch, computed } from 'vue'

const props = defineProps({
  startDate: {
    type: String,
    required: true
  },
  endDate: {
    type: String,
    required: true
  }
})

const emit = defineEmits(['update:startDate', 'update:endDate'])

const selectedQuick = ref('')

const quickOptions = [
  { label: '今天', value: 'today' },
  { label: '昨天', value: 'yesterday' },
  { label: '近7天', value: 'last7days' },
  { label: '近30天', value: 'last30days' }
]

const formatDate = (date) => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const selectQuick = (value) => {
  selectedQuick.value = value
  const today = new Date()
  let start, end

  switch (value) {
    case 'today':
      start = end = today
      break
    case 'yesterday': {
      const yesterday = new Date(today)
      yesterday.setDate(yesterday.getDate() - 1)
      start = end = yesterday
      break
    }
    case 'last7days': {
      end = new Date(today)
      start = new Date(today)
      start.setDate(start.getDate() - 6)
      break
    }
    case 'last30days': {
      end = new Date(today)
      start = new Date(today)
      start.setDate(start.getDate() - 29)
      break
    }
  }

  emit('update:startDate', formatDate(start))
  emit('update:endDate', formatDate(end))
}

const onStartDateChange = (e) => {
  selectedQuick.value = ''
  emit('update:startDate', e.target.value)
}

const onEndDateChange = (e) => {
  selectedQuick.value = ''
  emit('update:endDate', e.target.value)
}

watch([() => props.startDate, () => props.endDate], ([start, end]) => {
  const today = formatDate(new Date())
  const yesterday = formatDate(new Date(new Date().setDate(new Date().getDate() - 1)))
  
  if (start === today && end === today) {
    selectedQuick.value = 'today'
  } else if (start === yesterday && end === yesterday) {
    selectedQuick.value = 'yesterday'
  } else {
    const startDiff = Math.floor((new Date(today) - new Date(start)) / (1000 * 60 * 60 * 24))
    const endDiff = Math.floor((new Date(today) - new Date(end)) / (1000 * 60 * 60 * 24))
    
    if (endDiff === 0 && startDiff === 6) {
      selectedQuick.value = 'last7days'
    } else if (endDiff === 0 && startDiff === 29) {
      selectedQuick.value = 'last30days'
    } else {
      selectedQuick.value = ''
    }
  }
}, { immediate: true })
</script>

<style scoped>
.date-picker {
  display: flex;
  align-items: center;
  gap: 16px;
}

.quick-buttons {
  display: flex;
  gap: 8px;
}

.quick-btn {
  padding: 6px 12px;
  background: var(--color-bg-section);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  color: var(--color-text-secondary);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s;
}

.quick-btn:hover {
  border-color: var(--color-text-secondary);
}

.quick-btn.active {
  background: var(--color-text-primary);
  border-color: var(--color-text-primary);
  color: #fff;
}

.date-range {
  display: flex;
  align-items: center;
  gap: 8px;
}

.date-input {
  padding: 6px 10px;
  background: var(--color-bg-section);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  color: var(--color-text-primary);
  font-size: 13px;
  cursor: pointer;
}

.date-input:focus {
  outline: none;
  border-color: var(--color-text-secondary);
}

.date-separator {
  color: var(--color-text-tertiary);
  font-size: 13px;
}
</style>
