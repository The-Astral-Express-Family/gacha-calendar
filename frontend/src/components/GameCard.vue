<script setup lang="ts">
import { computed } from 'vue'
import ConfidenceBadge from '@/components/ConfidenceBadge.vue'
import CoverImage from '@/components/CoverImage.vue'
import UiIcon from '@/components/UiIcon.vue'
import type { Game } from '@/lib/api'
import { gameColor } from '@/lib/games'
import { tzZone } from '@/stores/tz'
import { formatDateShort, formatTime, formatDateTime } from '@/lib/time'

const props = defineProps<{ game: Game }>()
const events = computed(() => [
  {
    kind: 'preview',
    label: '前瞻直播',
    time: props.game.prediction?.preview,
    confidence: props.game.prediction?.preview_confidence,
    evidence: props.game.prediction?.preview_evidence,
  },
  {
    kind: 'update',
    label: props.game.prediction?.is_phase_update ? '阶段更新' : '版本更新',
    time: props.game.prediction?.update,
    confidence: props.game.prediction?.update_confidence,
    evidence: props.game.prediction?.update_evidence,
  },
])
const statusText = computed(() =>
  props.game.status === 'error' ? '采集异常' : props.game.status === 'partial' ? '部分采集失败' : '数据已同步'
)
</script>

<template>
  <article class="game-card" :style="{ '--game': gameColor(game.id) }">
    <div class="game-visual">
      <CoverImage :src="game.cover" :fallback="game.fallback" :alt="`${game.name} 主视觉`" />
      <div class="visual-shade" />
      <span v-if="game.prediction?.version" class="version-tag">{{ game.prediction.version }}</span>
      <a
        :href="game.bilibili_space"
        target="_blank"
        rel="noreferrer"
        class="official-link"
        :aria-label="`${game.name} B站官方账号`"
        title="B站官方账号"
        ><UiIcon name="arrow" :size="18"
      /></a>
      <div class="visual-title">
        <span v-if="game.prediction?.version_name" class="version-name">{{ game.prediction.version_name }}</span>
        <h3>{{ game.name }}</h3>
      </div>
    </div>
    <div class="game-schedule">
      <div v-for="event in events" :key="event.kind" class="schedule-row">
        <span class="event-label"><UiIcon :name="event.kind" :size="14" />{{ event.label }}</span>
        <template v-if="event.time">
          <time :datetime="event.time.utc" :title="formatDateTime(tzZone, event.time.unix_seconds)"
            >{{ formatDateShort(tzZone, event.time.unix_seconds)
            }}<span>{{ formatTime(tzZone, event.time.unix_seconds) }}</span></time
          >
          <ConfidenceBadge :value="event.confidence" />
        </template>
        <span v-else class="pending">{{
          event.kind === 'preview' && game.prediction?.is_phase_update ? '本阶段无前瞻' : '待公布'
        }}</span>
      </div>
    </div>
    <div class="card-bottom">
      <span
        class="collection-state"
        :class="{ warning: game.status !== 'ok' }"
        :title="game.last_error || statusText"
        :aria-label="statusText"
        ><i /><span v-if="game.status !== 'ok'">{{ statusText }}</span></span
      >
      <details v-if="game.prediction" class="evidence">
        <summary title="查看时间依据">时间依据<UiIcon name="info" :size="12" /></summary>
        <div class="evidence-content">
          <p v-for="event in events.filter((item) => item.evidence)" :key="event.kind">
            <strong>{{ event.label }}</strong
            >{{ event.evidence }}
          </p>
          <a
            v-if="game.prediction.announcement_url"
            :href="game.prediction.announcement_url"
            target="_blank"
            rel="noreferrer"
            >查看原始公告 <UiIcon name="arrow" :size="12"
          /></a>
        </div>
      </details>
    </div>
  </article>
</template>

<style scoped>
.game-card {
  min-width: 0;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
}
.game-visual {
  position: relative;
  aspect-ratio: 16 / 10;
  overflow: hidden;
  border-radius: 11px 11px 0 0;
  background: var(--bg-soft);
}
.game-visual > img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.visual-shade {
  position: absolute;
  inset: 0;
  background: linear-gradient(180deg, #080c1833, transparent 36%, #080c18c9);
  pointer-events: none;
}
.version-tag {
  position: absolute;
  top: 15px;
  left: 16px;
  padding: 3px 10px;
  border: 1px solid #ffffff3b;
  border-radius: 5px;
  background: #0b10185e;
  color: #fff;
  backdrop-filter: blur(12px);
  font-size: 14px;
  font-variant-numeric: tabular-nums;
}
.official-link {
  position: absolute;
  right: 13px;
  top: 13px;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border: 1px solid #ffffff33;
  border-radius: 50%;
  color: #fff;
  background: #0b101859;
  backdrop-filter: blur(12px);
}
.official-link:hover {
  background: #0b1018b3;
}
.visual-title {
  position: absolute;
  left: 20px;
  right: 20px;
  bottom: 19px;
}
.visual-title h3 {
  margin: 3px 0 0;
  color: #fff;
  font-size: clamp(18px, 1.7vw, 24px);
  font-weight: 600;
  letter-spacing: 0.03em;
  text-shadow: 0 2px 15px #0008;
}
.version-name {
  display: block;
  color: #ffffffe0;
  font-size: 11px;
}
.game-schedule {
  display: flex;
  flex-direction: column;
  gap: 15px;
  padding: 20px 18px 16px;
}
.schedule-row {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}
.event-label {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11px;
  color: var(--text-faint);
  white-space: nowrap;
}
.event-label > svg {
  color: var(--game);
}
.schedule-row time {
  margin-left: auto;
  display: inline-flex;
  gap: 9px;
  font-size: 15px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.schedule-row time span {
  font-size: 12px;
  color: var(--text-dim);
  align-self: center;
}
.pending {
  margin-left: auto;
  font-size: 12px;
  color: var(--text-faint);
}
.card-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 0 18px;
  padding: 11px 0;
  border-top: 1px solid var(--border);
  font-size: 10px;
  color: var(--text-faint);
}
.collection-state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.collection-state i {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--official);
}
.collection-state.warning {
  color: var(--inferred);
}
.collection-state.warning i {
  background: var(--inferred);
}
.evidence {
  position: relative;
}
.evidence summary {
  display: flex;
  align-items: center;
  gap: 6px;
  list-style: none;
  cursor: pointer;
  padding: 3px 0;
}
.evidence summary::-webkit-details-marker {
  display: none;
}
.evidence[open] summary {
  color: var(--accent);
}
.evidence-content {
  position: absolute;
  right: 0;
  bottom: calc(100% + 12px);
  z-index: 10;
  width: min(300px, 78vw);
  padding: 16px;
  background: #252d38;
  border: 1px solid #4c596b;
  border-radius: 9px;
  box-shadow: 0 10px 30px #0006;
  color: var(--text-dim);
  font-size: 12px;
}
.evidence-content p {
  margin: 0 0 12px;
  overflow-wrap: anywhere;
}
.evidence-content strong {
  display: block;
  margin-bottom: 5px;
  color: var(--text);
  font-weight: 500;
}
@media (max-width: 640px) {
  .game-visual {
    aspect-ratio: 16 / 10;
  }
  .visual-title h3 {
    font-size: 23px;
  }
  .game-schedule {
    padding-block: 18px;
  }
}
</style>
