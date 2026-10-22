<script setup lang="ts">
// Event main visual: prefer the collected local image, but also accept a
// remote CDN URL (sent without a referer, which the image CDNs allow);
// on any load failure fall back to the per-game default SVG.
import { ref } from 'vue'

const props = defineProps<{
  src?: string
  fallback: string
  alt: string
}>()

const failed = ref(false)
const current = ref(props.src || '')
</script>

<template>
  <img
    v-if="current && !failed"
    :src="current"
    :alt="alt"
    loading="lazy"
    referrerpolicy="no-referrer"
    @error="failed = true"
  />
  <img v-else :src="fallback" :alt="alt" loading="lazy" />
</template>
