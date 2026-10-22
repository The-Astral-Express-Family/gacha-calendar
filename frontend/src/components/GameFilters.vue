<script setup lang="ts">
import type { Game } from '@/lib/api'
import { gameColor, shortName } from '@/lib/games'

defineProps<{ games: Game[]; selected: string[] }>()
const emit = defineEmits<{
  (e: 'toggle', id: string): void
  (e: 'clear'): void
}>()
</script>

<template>
  <div class="game-filters" role="group" aria-label="筛选游戏">
    <button
      class="chip"
      :class="{ active: selected.length === 0 }"
      :aria-pressed="selected.length === 0"
      @click="emit('clear')"
    >
      全部游戏
    </button>
    <button
      v-for="game in games"
      :key="game.id"
      class="chip"
      :class="{ active: selected.includes(game.id) }"
      :aria-pressed="selected.includes(game.id)"
      :style="{ '--chip-color': gameColor(game.id) }"
      :title="game.name"
      @click="emit('toggle', game.id)"
    >
      <span class="dot" :style="{ background: gameColor(game.id) }" />{{ shortName(game) }}
    </button>
  </div>
</template>

<style scoped>
.game-filters {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  min-width: 0;
}
@media (max-width: 760px) {
  .game-filters {
    flex-wrap: nowrap;
    overflow-x: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--border) transparent;
    padding-bottom: 4px;
  }
}
</style>
