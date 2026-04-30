<template>
  <div class="stat-card">
    <div class="stat-value">{{ formattedValue }}</div>
    <div class="stat-label">{{ label }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatMoney, formatNumber } from '../../utils/format'

const props = defineProps({
  value: {
    type: [Number, String],
    default: 0
  },
  label: {
    type: String,
    required: true
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
  background: var(--color-bg-section);
  padding: 20px;
}

.stat-value {
  font-size: 28px;
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: 8px;
  line-height: 1.2;
}

.stat-label {
  font-size: 13px;
  color: var(--color-text-secondary);
}
</style>
