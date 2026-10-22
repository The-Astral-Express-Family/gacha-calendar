<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import TzSwitcher from '@/components/TzSwitcher.vue'
import DashboardView from '@/views/DashboardView.vue'
import PostsView from '@/views/PostsView.vue'
import type { Meta } from '@/lib/api'
import { fetchMeta } from '@/lib/api'
import { navigate, routeState } from '@/lib/router'
import { tzOffset, tzZone } from '@/stores/tz'
import { formatDateTime } from '@/lib/time'

const meta = ref<Meta | null>(null)
const shareHint = ref('')

onMounted(async () => {
  try {
    meta.value = await fetchMeta()
  } catch {
    /* meta is decorative; the page still works */
  }
})

const lastCycleText = computed(() => {
  if (!meta.value?.refresh.last_cycle_at) return '尚未采集'
  return formatDateTime(tzZone.value, Date.parse(meta.value.refresh.last_cycle_at) / 1000)
})

async function share() {
  const url = new URL(window.location.href)
  url.searchParams.set('tz', String(localStorage.getItem('gacha-calendar.tz') ?? 'local'))
  try {
    await navigator.clipboard.writeText(url.toString())
    shareHint.value = '链接已复制（含时区设置）'
  } catch {
    shareHint.value = url.toString()
  }
  setTimeout(() => {
    shareHint.value = ''
  }, 2500)
}
</script>

<template>
  <header class="topbar">
    <div class="page topbar-inner">
      <div class="brand" @click="navigate('dashboard')">
        <img src="/favicon.svg" alt="" class="logo" />
        <div>
          <div class="brand-name">二游版本日历</div>
          <div class="brand-sub">gacha-calendar · 更新与前瞻时间预测</div>
        </div>
      </div>
      <nav class="tabs">
        <button :class="{ active: routeState.name === 'dashboard' }" @click="navigate('dashboard')">时间线</button>
        <button :class="{ active: routeState.name === 'posts' }" @click="navigate('posts')">官方公告</button>
        <TzSwitcher />
        <button class="ghost" title="复制带时区参数的链接" @click="share">分享</button>
      </nav>
    </div>
    <div v-if="shareHint" class="page share-hint">{{ shareHint }}</div>
  </header>

  <main class="page">
    <DashboardView v-if="routeState.name === 'dashboard'" :meta="meta" :last-cycle-text="lastCycleText" />
    <PostsView v-else />
  </main>

  <footer class="page footer">
    <template v-if="meta">
      <span class="foot-long"
        >口径：{{ meta.timezone.business }}（{{ meta.timezone.utc_offset }}），数据均为 UTC
        存储/返回，页面按所选时区渲染</span
      >
      <span>当前视图 {{ tzZone }}（{{ tzOffset }}）</span>
      <span>上次采集：{{ lastCycleText }} · {{ meta.version }}</span>
    </template>
    <template v-else>gacha-calendar</template>
  </footer>
</template>

<style scoped>
.topbar {
  position: sticky;
  top: 0;
  z-index: 30;
  backdrop-filter: blur(16px);
  background: rgba(11, 17, 30, 0.82);
  border-bottom: 1px solid var(--border);
}
.topbar-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.8rem;
  padding: 0.6rem 0;
  flex-wrap: wrap;
}
.brand {
  display: flex;
  gap: 0.65rem;
  align-items: center;
  cursor: pointer;
  min-width: 0;
}
.logo {
  width: 32px;
  height: 32px;
  flex-shrink: 0;
}
.brand-name {
  font-weight: 700;
  white-space: nowrap;
}
.brand-sub {
  font-size: 0.72rem;
  color: var(--text-faint);
  white-space: nowrap;
}
.tabs {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  flex-wrap: wrap;
}
.tabs button.active {
  background: var(--accent-soft);
  border-color: var(--accent);
  color: var(--text);
}
.tabs button.ghost {
  color: var(--text-dim);
}
.share-hint {
  padding: 0.2rem 0 0.5rem;
  font-size: 0.78rem;
  color: var(--official);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.footer {
  display: flex;
  gap: 1.4rem;
  flex-wrap: wrap;
  padding: 1.1rem 0 1.4rem;
  font-size: 0.74rem;
  color: var(--text-faint);
}
@media (max-width: 760px) {
  .brand-sub {
    display: none;
  }
  .tabs {
    width: 100%;
    justify-content: space-between;
  }
  .tabs button {
    padding: 0.4rem 0.9rem;
  }
  .footer {
    gap: 0.5rem 1rem;
    flex-direction: column;
  }
  .foot-long {
    display: none;
  }
}
</style>
