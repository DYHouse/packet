<template>
  <div class="rank-table">
    <div class="table-title">{{ title }}</div>
    <table class="data-table">
      <thead>
        <tr>
          <th class="col-rank">排名</th>
          <th class="col-name">房间名称</th>
          <th>局数</th>
          <th>玩家</th>
          <th>流水</th>
          <th>顺子</th>
          <th>豹子</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(item, index) in data" :key="item.room_id">
          <td class="col-rank">{{ index + 1 }}</td>
          <td class="col-name">{{ item.room_name || '未命名' }}</td>
          <td>{{ item.round_count }}</td>
          <td>{{ item.player_count }}</td>
          <td>{{ formatMoney(item.total_amount) }}</td>
          <td>{{ item.straight_count }}</td>
          <td>{{ item.leopard_count }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup>
import { formatMoney } from '../../utils/format'

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
  background: var(--color-bg-section);
}

.table-title {
  padding: 16px 20px;
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
  border-bottom: 1px solid var(--color-border);
}

.data-table {
  width: 100%;
  border-collapse: collapse;
}

.data-table th,
.data-table td {
  padding: 12px 16px;
  text-align: center;
  font-size: 13px;
}

.data-table th {
  color: var(--color-text-secondary);
  font-weight: 500;
  background: var(--color-bg);
  border-bottom: 1px solid var(--color-border);
}

.data-table td {
  color: var(--color-text-primary);
  border-bottom: 1px solid var(--color-border-light);
}

.data-table tbody tr:hover {
  background: var(--color-bg);
}

.data-table tbody tr:last-child td {
  border-bottom: none;
}

.col-rank {
  width: 60px;
}

.col-name {
  text-align: left;
}
</style>
