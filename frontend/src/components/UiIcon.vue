<script setup lang="ts">
defineProps<{ name: string; size?: number }>()

const paths: Record<string, string> = {
  calendar: 'M8 2v4m8-4v4M3 10h18M5 4h14a2 2 0 0 1 2 2v14H3V6a2 2 0 0 1 2-2Zm3 10h2m4 0h2m-8 4h2',
  timeline: 'M4 6h16M4 12h16M4 18h16M8 4v4m8 2v4M10 16v4',
  grid: 'M3 3h7v7H3zm11 0h7v7h-7zM3 14h7v7H3zm11 0h7v7h-7z',
  arrow: 'M7 17 17 7M7 7h10v10',
  chevron: 'm9 5 7 7-7 7',
  close: 'm6 6 12 12M6 18 18 6',
  globe: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM3 12h18M12 3a18 18 0 0 1 0 18 18 18 0 0 1 0-18Z',
  share:
    'M9 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm12-7a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm0 14a3 3 0 1 1-6 0 3 3 0 0 1 6 0ZM9 10.5l6-4M9 13.5l6 4',
  update: 'm12 3 9 9-9 9-9-9Z',
  preview: 'M4 6h16v14H4zM8 2l4 4 4-4M10 10l5 3-5 3Z',
  clock: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Zm-9-5v5l3 2',
  search: 'M17 10a7 7 0 1 1-14 0 7 7 0 0 1 14 0Zm-2 5 6 6',
  filter: 'M4 6h16M7 12h10m-7 6h4M8 4v4m8 2v4m-4 2v4',
  check: 'm5 12 4 4L19 6',
  info: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Zm-9-1v6m0-10v.01',
  refresh: 'M20 7v5h-5M4 17v-5h5M5.4 7a8 8 0 0 1 13.2-1L20 8M4 16l1.4 2A8 8 0 0 0 18.6 17',
  play: 'm9 5 11 7-11 7Z',
  article: 'M5 3h14v18H5zM9 7h6m-6 5h6m-6 5h4',
  message: 'M21 4H3v13h5l4 4v-4h9Z',
}
</script>

<template>
  <svg
    :width="size || 18"
    :height="size || 18"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    class="ui-icon"
  >
    <path :d="paths[name] || paths.info" />
  </svg>
</template>

<style scoped>
.ui-icon {
  flex-shrink: 0;
  vertical-align: middle;
}
</style>
