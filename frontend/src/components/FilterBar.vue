<script setup lang="ts">
import type { Game } from '@/lib/api'
import { gameColor } from '@/lib/games'

defineProps<{
  games: Game[]
  selected: string[]
  range: number
}>()

const emit = defineEmits<{
  (e: 'toggle-game', id: string): void
  (e: 'range', days: number): void
  (e: 'all-games'): void
}>()

const RANGES: Array<{ value: number; label: string }> = [
  { value: 14, label: '14 天' },
  { value: 30, label: '30 天' },
  { value: 90, label: '90 天' },
  { value: 3650, label: '全部' },
]
</script>

<template>
  <div class="filter-bar">
    <div class="chips">
      <button class="chip" :class="{ active: selected.length === 0 }" @click="emit('all-games')">全部游戏</button>
      <button
        v-for="game in games"
        :key="game.id"
        class="chip"
        :class="{ active: selected.includes(game.id) }"
        :style="{ '--chip-color': gameColor(game.id) }"
        @click="emit('toggle-game', game.id)"
      >
        <span class="dot" :style="{ background: gameColor(game.id) }" />
        {{ game.name }}
      </button>
    </div>
    <div class="controls">
      <label class="control">
        范围
        <select :value="range" @change="emit('range', Number(($event.target as HTMLSelectElement).value))">
          <option v-if="range === 0" :value="0">自定义</option>
          <option v-for="option in RANGES" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </label>
    </div>
  </div>
</template>

<style scoped>
.filter-bar {
  display: flex;
  gap: 0.8rem;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  margin-bottom: 0.9rem;
}
.chips {
  display: flex;
  gap: 0.45rem;
  flex-wrap: wrap;
  min-width: 0;
}
.controls {
  display: flex;
  gap: 0.7rem;
  flex-wrap: wrap;
}
.control {
  display: inline-flex;
  gap: 0.4rem;
  align-items: center;
  font-size: 0.8rem;
  color: var(--text-dim);
  white-space: nowrap;
}
@media (max-width: 760px) {
  .filter-bar {
    flex-direction: column;
    align-items: stretch;
  }
  .chips {
    flex-wrap: nowrap;
    overflow-x: auto;
    width: 100%;
    max-width: 100%;
    padding-bottom: 0.3rem;
    scrollbar-width: none;
  }
  .chips::-webkit-scrollbar {
    display: none;
  }
  .controls {
    justify-content: flex-end;
  }
}
</style>
