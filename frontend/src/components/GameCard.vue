<script setup lang="ts">
import { computed } from 'vue'

import ConfidenceBadge from '@/components/ConfidenceBadge.vue'
import CoverImage from '@/components/CoverImage.vue'
import type { Game } from '@/lib/api'
import { gameColor } from '@/lib/games'
import { tzZone } from '@/stores/tz'
import { formatShort, formatTime, relativeTime } from '@/lib/time'

const props = defineProps<{ game: Game }>()

const nowUnix = computed(() => Math.floor(Date.now() / 1000))

const update = computed(() => props.game.prediction?.update)
const preview = computed(() => props.game.prediction?.preview)

const countdownTarget = computed(() => {
  const candidates = [update.value, preview.value]
    .filter((item): item is NonNullable<typeof item> => Boolean(item))
    .sort((a, b) => a.unix_seconds - b.unix_seconds)
  const upcoming = candidates.find((item) => item.unix_seconds >= nowUnix.value)
  return upcoming ?? candidates[0]
})

const statusText = computed(() => {
  switch (props.game.status) {
    case 'ok': return '正常'
    case 'partial': return '部分失败'
    case 'error': return '采集异常'
    default: return props.game.status
  }
})
</script>

<template>
  <article class="game-card panel" :style="{ '--game': gameColor(game.id) }">
    <div class="cover-wrap">
      <CoverImage :src="game.cover" :fallback="game.fallback" :alt="`${game.name} 主视觉`" />
      <div class="cover-shade" />
      <div class="cover-title">
        <span class="game-name">{{ game.name }}</span>
        <span v-if="game.prediction?.version" class="version">v{{ game.prediction.version }}</span>
      </div>
      <span class="status" :class="game.status" :title="game.last_error || ''">{{ statusText }}</span>
    </div>
    <div class="body">
      <div class="event preview">
        <span class="label">前瞻直播</span>
        <template v-if="preview">
          <span class="when">{{ formatShort(tzZone, preview.unix_seconds) }}</span>
          <span class="meta">
            {{ formatTime('Asia/Shanghai', preview.unix_seconds) }} 国服 ·
            {{ relativeTime(preview.unix_seconds, nowUnix) }}
          </span>
        </template>
        <span v-else class="meta">暂无预测</span>
        <ConfidenceBadge :value="game.prediction?.preview_confidence" />
      </div>
      <div class="event update">
        <span class="label">版本更新</span>
        <template v-if="update">
          <span class="when">{{ formatShort(tzZone, update.unix_seconds) }}</span>
          <span class="meta">
            {{ formatTime('Asia/Shanghai', update.unix_seconds) }} 国服 ·
            {{ relativeTime(update.unix_seconds, nowUnix) }}
          </span>
        </template>
        <span v-else class="meta">暂无预测</span>
        <ConfidenceBadge :value="game.prediction?.update_confidence" />
      </div>
      <div class="foot">
        <a :href="game.bilibili_space" target="_blank" rel="noreferrer">B 站官号 ↗</a>
        <span v-if="countdownTarget" class="countdown" :title="countdownTarget.utc">
          距最近事件 {{ relativeTime(countdownTarget.unix_seconds, nowUnix) }}
        </span>
      </div>
    </div>
  </article>
</template>

<style scoped>
.game-card {
  overflow: hidden;
  display: flex;
  flex-direction: column;
  border-color: color-mix(in srgb, var(--game) 30%, transparent);
}
.cover-wrap { position: relative; aspect-ratio: 16 / 8.2; overflow: hidden; }
.cover-wrap img { width: 100%; height: 100%; object-fit: cover; display: block; }
.cover-shade {
  position: absolute; inset: 0;
  background: linear-gradient(180deg, rgba(11,16,32,0.05) 30%, rgba(11,16,32,0.85));
}
.cover-title {
  position: absolute; left: 0.9rem; bottom: 0.7rem;
  display: flex; align-items: baseline; gap: 0.55rem;
}
.game-name { font-weight: 750; font-size: 1.06rem; text-shadow: 0 2px 10px rgba(0,0,0,0.6); }
.version { font-size: 0.8rem; color: #fff9; font-family: ui-monospace, monospace; }
.status {
  position: absolute; top: 0.6rem; right: 0.6rem;
  font-size: 0.68rem; padding: 0.1rem 0.5rem; border-radius: 999px;
  background: rgba(2, 6, 23, 0.55); border: 1px solid rgba(255, 255, 255, 0.16);
}
.status.ok { color: var(--official); border-color: rgba(52, 211, 153, 0.4); }
.status.partial { color: var(--inferred); border-color: rgba(251, 191, 36, 0.4); }
.status.error { color: var(--danger); border-color: rgba(248, 113, 113, 0.4); }
.body { padding: 0.85rem 0.95rem 0.9rem; display: flex; flex-direction: column; gap: 0.55rem; }
.event { display: grid; grid-template-columns: 4.2rem 1fr auto; align-items: baseline; gap: 0.4rem; }
.label { font-size: 0.74rem; color: var(--text-faint); }
.when { font-weight: 650; font-size: 0.94rem; }
.meta { grid-column: 2 / 4; font-size: 0.74rem; color: var(--text-dim); }
.foot {
  display: flex; justify-content: space-between; align-items: center;
  border-top: 1px solid var(--border); padding-top: 0.55rem; margin-top: 0.15rem;
  font-size: 0.78rem;
}
.countdown { color: var(--text-dim); }
</style>
