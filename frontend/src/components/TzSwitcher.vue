<script setup lang="ts">
import { computed } from 'vue'

import { setTz, tzOptions, tzState, tzZone, tzOffset } from '@/stores/tz'

const options = computed(() => tzOptions())
</script>

<template>
  <label class="tz-switcher" title="切换展示时区；也可通过链接 ?tz= 分享">
    <span class="tz-label">时区</span>
    <select :value="tzState.value" @change="setTz(($event.target as HTMLSelectElement).value)">
      <option v-for="option in options" :key="option.value" :value="option.value">
        {{ option.label }}
      </option>
    </select>
    <span class="tz-current">{{ tzZone }} {{ tzOffset }}</span>
  </label>
</template>

<style scoped>
.tz-switcher {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  font-size: 0.82rem;
}
.tz-label {
  color: var(--text-faint);
}
.tz-current {
  color: var(--text-faint);
  font-size: 0.72rem;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
@media (max-width: 760px) {
  .tz-label,
  .tz-current {
    display: none;
  }
}
</style>
