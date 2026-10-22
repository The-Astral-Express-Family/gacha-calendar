<script setup lang="ts">
import { computed } from 'vue'
import UiIcon from '@/components/UiIcon.vue'
import { setTz, tzOptions, tzState, tzZone, tzOffset } from '@/stores/tz'
const options = computed(() => tzOptions())
const label = computed(() => {
  if (tzZone.value === 'Asia/Shanghai') return '国服时间'
  if (tzZone.value === 'UTC') return 'UTC'
  return tzZone.value.split('/').pop()?.replaceAll('_', ' ') || '本地时间'
})
</script>
<template>
  <label class="tz-switcher" :title="`${tzZone} · ${tzOffset}`">
    <UiIcon name="globe" :size="15" /><span>{{ label }}</span
    ><UiIcon name="chevron" :size="11" class="select-chevron" />
    <select aria-label="显示时区" :value="tzState.value" @change="setTz(($event.target as HTMLSelectElement).value)">
      <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
    </select>
  </label>
</template>
<style scoped>
.tz-switcher {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-height: 38px;
  color: var(--text-dim);
  font-size: 12px;
}
.tz-switcher select {
  position: absolute;
  inset: 0;
  width: 100%;
  opacity: 0;
  cursor: pointer;
}
.tz-switcher:focus-within {
  outline: 2px solid var(--accent);
  outline-offset: 5px;
  border-radius: 3px;
}
.select-chevron {
  transform: rotate(90deg);
  margin-left: 3px;
}
</style>
