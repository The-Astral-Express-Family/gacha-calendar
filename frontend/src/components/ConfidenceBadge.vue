<script setup lang="ts">
import { confidenceClass, confidenceLabel } from '@/lib/games'

defineProps<{ value?: string }>()
</script>

<template>
  <span class="badge" :class="confidenceClass(value)">{{ confidenceLabel(value) }}</span>
</template>
