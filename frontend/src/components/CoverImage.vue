<script setup lang="ts">
// Keep artwork visible when an individual post has no cover or its CDN fails:
// post artwork -> game artwork -> bundled placeholder. Never retry a failed URL.
import { computed, ref, watch } from 'vue'

const props = defineProps<{
  src?: string
  secondary?: string
  fallback: string
  alt: string
  eager?: boolean
}>()

const attempt = ref(0)
const sources = computed(() => [...new Set([props.src, props.secondary, props.fallback].filter(Boolean))])
watch(sources, () => {
  attempt.value = 0
})
function nextSource() {
  if (attempt.value < sources.value.length - 1) attempt.value++
}
</script>

<template>
  <img
    :src="sources[attempt]"
    :alt="alt"
    :loading="eager ? 'eager' : 'lazy'"
    :fetchpriority="eager ? 'high' : 'auto'"
    decoding="async"
    referrerpolicy="no-referrer"
    @error="nextSource"
  />
</template>
