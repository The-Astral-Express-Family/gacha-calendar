<script setup lang="ts">
import GameFilters from '@/components/GameFilters.vue'
import type { Game } from '@/lib/api'
defineProps<{ games: Game[]; selected: string[]; range: number }>()
const emit = defineEmits<{
  (e: 'toggle-game', id: string): void
  (e: 'range', days: number): void
  (e: 'all-games'): void
}>()
const ranges = [
  { value: 14, label: '两周' },
  { value: 30, label: '一个月' },
  { value: 90, label: '三个月' },
  { value: 3650, label: '全部' },
]
</script>
<template>
  <div class="filter-bar">
    <GameFilters :games="games" :selected="selected" @toggle="emit('toggle-game', $event)" @clear="emit('all-games')" />
    <div class="range-control" role="group" aria-label="时间范围">
      <span v-if="range === 0" class="custom-range">自定义</span>
      <button
        v-for="option in ranges"
        :key="option.value"
        :class="{ active: range === option.value }"
        :aria-pressed="range === option.value"
        @click="emit('range', option.value)"
      >
        {{ option.label }}
      </button>
    </div>
  </div>
</template>
<style scoped>
.filter-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 22px;
}
.range-control {
  display: inline-flex;
  gap: 2px;
  flex-shrink: 0;
  padding: 3px;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.range-control button {
  padding: 5px 10px;
  font-size: 11px;
  color: var(--text-faint);
  border-radius: 5px;
  background: transparent;
  white-space: nowrap;
}
.range-control button.active {
  color: var(--text);
  background: #354252;
}
.range-control button:hover {
  color: var(--text);
}
.custom-range {
  align-self: center;
  padding-inline: 8px;
  font-size: 11px;
  color: var(--accent);
}
@media (max-width: 1100px) {
  .filter-bar {
    align-items: stretch;
    flex-direction: column;
    gap: 12px;
  }
  .range-control {
    align-self: flex-end;
  }
}
@media (max-width: 640px) {
  .filter-bar {
    margin-bottom: 14px;
    gap: 8px;
  }
}
</style>
