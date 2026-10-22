<script setup lang="ts">
import { computed } from 'vue'

import UiIcon from '@/components/UiIcon.vue'
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
  <nav v-if="pagination.total_pages > 1" class="pagination" aria-label="公告分页">
    <span class="faint">{{ pagination.total }} 条公告</span>
    <div class="pages">
      <button :disabled="pagination.page <= 1" aria-label="上一页" @click="jump(pagination.page - 1)">
        <UiIcon name="chevron" :size="14" class="previous" />
      </button>
      <template v-for="(page, index) in pages" :key="`${page}-${index}`">
        <span v-if="page === '…'" class="faint">…</span>
        <button
          v-else
          :class="{ active: page === pagination.page }"
          :aria-label="`第 ${page} 页`"
          :aria-current="page === pagination.page ? 'page' : undefined"
          @click="jump(page)"
        >
          {{ page }}
        </button>
      </template>
      <button
        :disabled="pagination.page >= pagination.total_pages"
        aria-label="下一页"
        @click="jump(pagination.page + 1)"
      >
        <UiIcon name="chevron" :size="14" />
      </button>
    </div>
  </nav>
</template>

<style scoped>
.pagination {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
  margin-top: 32px;
  padding-top: 24px;
  border-top: 1px solid var(--border);
}
.pages {
  display: flex;
  gap: 0.3rem;
  align-items: center;
}
.pages button {
  min-width: 34px;
  height: 34px;
  padding: 0;
  font-size: 12px;
  background: transparent;
}
.previous {
  transform: rotate(180deg);
}
.pages button.active {
  background: var(--accent-soft);
  border-color: var(--accent);
}
</style>
