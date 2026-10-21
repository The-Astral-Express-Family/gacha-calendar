<script setup lang="ts">
import { computed } from 'vue'

import type { Pagination } from '@/lib/api'

const props = defineProps<{ pagination: Pagination }>()
const emit = defineEmits<{ (e: 'go', page: number): void }>()

const pages = computed(() => {
  const total = props.pagination.total_pages
  const current = props.pagination.page
  const out: Array<number | '…'> = []
  const push = (value: number | '…') => out.push(value)
  if (total <= 7) {
    for (let i = 1; i <= total; i += 1) push(i)
    return out
  }
  push(1)
  if (current > 3) push('…')
  for (let i = Math.max(2, current - 1); i <= Math.min(total - 1, current + 1); i += 1) push(i)
  if (current < total - 2) push('…')
  push(total)
  return out
})

function jump(target: number) {
  if (target >= 1 && target <= props.pagination.total_pages && target !== props.pagination.page) {
    emit('go', target)
  }
}
</script>

<template>
  <div class="pagination">
    <span class="faint">
      共 {{ pagination.total }} 条 · 第 {{ pagination.page }}/{{ pagination.total_pages }} 页
    </span>
    <div class="pages">
      <button :disabled="pagination.page <= 1" @click="jump(pagination.page - 1)">‹</button>
      <template v-for="(page, index) in pages" :key="`${page}-${index}`">
        <span v-if="page === '…'" class="faint">…</span>
        <button v-else :class="{ active: page === pagination.page }" @click="jump(page)">{{ page }}</button>
      </template>
      <button :disabled="pagination.page >= pagination.total_pages" @click="jump(pagination.page + 1)">›</button>
    </div>
  </div>
</template>

<style scoped>
.pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
  margin-top: 0.9rem;
}
.pages { display: flex; gap: 0.3rem; align-items: center; }
.pages button { min-width: 2rem; padding: 0.28rem 0.5rem; }
.pages button.active {
  background: var(--accent-soft);
  border-color: var(--accent);
}
</style>
