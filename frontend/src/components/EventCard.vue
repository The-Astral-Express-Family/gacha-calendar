<script setup lang="ts">
import ConfidenceBadge from '@/components/ConfidenceBadge.vue'
import CoverImage from '@/components/CoverImage.vue'
import UiIcon from '@/components/UiIcon.vue'
import { gameColor } from '@/lib/games'
import { nowUnix } from '@/stores/now'
import { tzZone } from '@/stores/tz'
import { formatDateShort, formatTime, formatWeekday, relativeTime } from '@/lib/time'

export interface ScheduleEvent {
  key: string
  gameId: string
  gameName: string
  kind: 'update' | 'preview'
  kindLabel?: string
  unix: number
  title: string
  version: string
  confidence?: string
  evidence?: string
  url?: string
  cover?: string
  fallback: string
}

defineProps<{ event: ScheduleEvent; featured?: boolean; compact?: boolean }>()
</script>

<template>
  <article class="event-card" :class="{ featured, compact }" :style="{ '--game': gameColor(event.gameId) }">
    <CoverImage :src="event.cover" :fallback="event.fallback" :alt="event.title" :eager="featured" class="event-art" />
    <div class="event-shade" />
    <div class="event-top">
      <span class="event-kind"
        ><UiIcon :name="event.kind" :size="13" />{{
          event.kindLabel || (event.kind === 'preview' ? '前瞻直播' : '版本更新')
        }}</span
      >
      <a
        v-if="event.url"
        :href="event.url"
        target="_blank"
        rel="noreferrer"
        class="event-source"
        :aria-label="`${event.title}官方公告`"
        title="查看官方公告"
        ><UiIcon name="arrow" :size="18"
      /></a>
    </div>
    <div class="event-content">
      <div v-if="featured" class="countdown">
        <span>距离下一场</span><strong>{{ relativeTime(event.unix, nowUnix) }}</strong>
      </div>
      <div class="event-name">
        <h3>{{ event.gameName }}</h3>
        <span v-if="event.version" class="event-version">{{ event.version }}</span>
      </div>
      <div class="event-datetime">
        <time :datetime="new Date(event.unix * 1000).toISOString()">
          <span>{{ formatDateShort(tzZone, event.unix) }}</span>
          <span class="event-weekday">{{ formatWeekday(tzZone, event.unix) }}</span>
          <span>{{ formatTime(tzZone, event.unix) }}</span>
        </time>
        <ConfidenceBadge :value="event.confidence" />
      </div>
    </div>
  </article>
</template>

<style scoped>
.event-card {
  position: relative;
  min-width: 0;
  overflow: hidden;
  min-height: 190px;
  border: 1px solid #ffffff14;
  border-radius: 12px;
  background: var(--panel);
  isolation: isolate;
}
.event-art {
  position: absolute;
  inset: 0;
  height: 100%;
  width: 100%;
  object-fit: cover;
  z-index: -2;
}
.event-shade {
  position: absolute;
  inset: 0;
  background: linear-gradient(180deg, #09111a29, transparent 20%, #09111ad9 100%);
  z-index: -1;
}
.event-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px;
}
.event-kind {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: #fff;
  font-size: 10px;
  background: #10182187;
  border: 1px solid #ffffff2b;
  padding: 4px 8px;
  border-radius: 5px;
  backdrop-filter: blur(10px);
}
.event-source {
  color: #fff;
  background: #10182170;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: grid;
  place-items: center;
}
.event-content {
  position: absolute;
  bottom: 22px;
  left: 24px;
  right: 24px;
  color: #fff;
}
.event-name {
  display: flex;
  align-items: baseline;
  gap: 12px;
}
.event-name h3 {
  font-size: 21px;
  font-weight: 550;
  margin: 0 0 7px;
  letter-spacing: 0.03em;
  text-shadow: 0 2px 10px #0005;
}
.event-version {
  font-size: 14px;
  color: #ffffffbd;
}
.event-datetime {
  display: flex;
  align-items: center;
  gap: 12px;
}
.event-datetime time {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.event-weekday {
  color: #ffffffb0;
  font-size: 10px;
}
.event-datetime :deep(.badge) {
  background: #10182199;
}
.featured {
  min-height: 420px;
}
.featured .event-shade {
  background: linear-gradient(90deg, #09111a8c, transparent 75%), linear-gradient(0deg, #09111ae6, transparent 70%);
}
.featured .event-content {
  bottom: 32px;
  left: 34px;
  right: 34px;
}
.featured .event-top {
  padding: 24px 30px;
}
.featured .event-name h3 {
  font-size: clamp(26px, 2.5vw, 36px);
  margin-bottom: 12px;
}
.featured .event-version {
  font-size: 21px;
}
.featured .event-datetime time {
  font-size: 16px;
  gap: 14px;
}
.featured .event-weekday {
  font-size: 12px;
}
.countdown {
  display: flex;
  flex-direction: column;
  margin-bottom: 28px;
  gap: 4px;
}
.countdown > span {
  color: #ffffffb0;
  font-size: 11px;
}
.countdown strong {
  font-size: clamp(28px, 3vw, 42px);
  font-weight: 450;
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
}
.compact {
  min-height: 180px;
}
.compact .event-content {
  bottom: 17px;
  left: 18px;
  right: 18px;
}
.compact .event-name h3 {
  font-size: 17px;
}
.compact .event-version {
  font-size: 12px;
}
@media (max-width: 640px) {
  .featured {
    min-height: 360px;
  }
  .featured .event-top {
    padding: 18px;
  }
  .featured .event-content {
    left: 22px;
    right: 22px;
    bottom: 25px;
  }
  .featured .event-name h3 {
    font-size: 28px;
  }
  .countdown {
    margin-bottom: 24px;
  }
  .countdown strong {
    font-size: 36px;
  }
  .featured .event-datetime time {
    font-size: 14px;
    gap: 10px;
  }
  .event-content {
    left: 18px;
    right: 18px;
    bottom: 18px;
  }
  .event-name h3 {
    font-size: 18px;
  }
}
</style>
