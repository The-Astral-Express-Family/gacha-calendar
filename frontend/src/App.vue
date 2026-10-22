<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import TzSwitcher from '@/components/TzSwitcher.vue'
import UiIcon from '@/components/UiIcon.vue'
import DashboardView from '@/views/DashboardView.vue'
import PostsView from '@/views/PostsView.vue'
import type { Meta } from '@/lib/api'
import { fetchMeta } from '@/lib/api'
import { navigate, routeState } from '@/lib/router'
import { tzOffset, tzState, tzZone } from '@/stores/tz'
import { formatDateTime } from '@/lib/time'

const meta = ref<Meta | null>(null)
const shareHint = ref('')
let shareTimer: ReturnType<typeof setTimeout> | undefined
onMounted(async () => {
  try {
    meta.value = await fetchMeta()
  } catch {
    /* Schedule requests have their own visible error state. */
  }
})
onBeforeUnmount(() => clearTimeout(shareTimer))
const lastCycleText = computed(() => {
  if (!meta.value?.refresh.last_cycle_at) return ''
  return formatDateTime(tzZone.value, Date.parse(meta.value.refresh.last_cycle_at) / 1000)
})
async function share() {
  const url = new URL(window.location.href)
  url.searchParams.set('tz', tzState.value)
  try {
    await navigator.clipboard.writeText(url.toString())
    shareHint.value = '链接已复制'
  } catch {
    shareHint.value = url.toString()
  }
  clearTimeout(shareTimer)
  shareTimer = setTimeout(() => {
    shareHint.value = ''
  }, 4000)
}
</script>

<template>
  <a class="skip-link" href="#main">跳至内容</a>
  <header class="topbar">
    <div class="page topbar-inner">
      <a class="brand" href="/" @click.prevent="navigate('dashboard')" aria-label="二游日历首页">
        <span class="brand-mark"><UiIcon name="calendar" :size="23" /></span>
        <span class="brand-name">二游<span>日历</span></span>
      </a>
      <nav class="tabs" aria-label="主导航">
        <a
          href="/"
          :class="{ active: routeState.name === 'dashboard' }"
          :aria-current="routeState.name === 'dashboard' ? 'page' : undefined"
          @click.prevent="navigate('dashboard')"
          ><UiIcon name="timeline" :size="17" />版本日程</a
        >
        <a
          href="/posts"
          :class="{ active: routeState.name === 'posts' }"
          :aria-current="routeState.name === 'posts' ? 'page' : undefined"
          @click.prevent="navigate('posts')"
          ><UiIcon name="grid" :size="16" />官方公告</a
        >
      </nav>
      <div class="topbar-actions">
        <TzSwitcher />
        <button class="icon-button share-button" title="复制分享链接" aria-label="复制分享链接" @click="share">
          <UiIcon name="share" :size="17" />
        </button>
      </div>
    </div>
    <div v-if="shareHint" class="share-hint" role="status">{{ shareHint }}</div>
  </header>
  <main id="main" class="page">
    <DashboardView v-if="routeState.name === 'dashboard'" />
    <PostsView v-else />
  </main>
  <footer class="page footer">
    <span>二游日历</span>
    <div class="footer-meta">
      <span v-if="lastCycleText" :title="`上次采集 ${lastCycleText}`" class="sync-status"
        ><i :class="{ warning: meta?.refresh.last_cycle_ok !== meta?.refresh.last_cycle_all }" />{{
          lastCycleText
        }}
        更新</span
      >
      <span :title="tzZone">{{ tzOffset }}</span>
    </div>
  </footer>
</template>

<style scoped>
.skip-link {
  position: fixed;
  top: -60px;
  left: 20px;
  padding: 10px 16px;
  background: var(--accent);
  color: var(--bg);
  z-index: 100;
}
.skip-link:focus {
  top: 10px;
}
.topbar {
  position: sticky;
  top: 0;
  z-index: 30;
  background: #14181eef;
  backdrop-filter: blur(20px);
  border-bottom: 1px solid var(--border);
}
.topbar-inner {
  display: flex;
  align-items: center;
  gap: 56px;
  min-height: 78px;
}
.brand {
  display: inline-flex;
  align-items: center;
  gap: 11px;
  color: var(--text);
  flex-shrink: 0;
}
.brand-mark {
  display: grid;
  place-items: center;
  width: 37px;
  height: 40px;
  color: var(--accent);
  border: 1px solid #a9d5f53b;
  border-radius: 10px 10px 10px 3px;
}
.brand-name {
  font-size: 21px;
  font-weight: 650;
  letter-spacing: 0.06em;
}
.brand-name > span {
  color: var(--text-dim);
  font-weight: 400;
}
.tabs {
  display: flex;
  align-self: stretch;
  gap: 32px;
}
.tabs a {
  display: flex;
  align-items: center;
  gap: 8px;
  position: relative;
  color: var(--text-faint);
  font-size: 13px;
}
.tabs a.active {
  color: var(--text);
}
.tabs a.active::after {
  position: absolute;
  content: '';
  height: 2px;
  background: var(--accent);
  inset: auto 0 -1px;
}
.topbar-actions {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-left: auto;
}
.share-hint {
  position: absolute;
  right: 24px;
  top: calc(100% + 10px);
  max-width: min(90vw, 500px);
  overflow-wrap: anywhere;
  padding: 12px 18px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--panel-strong);
  box-shadow: 0 12px 30px #0004;
  font-size: 13px;
}
.footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 20px;
  padding-block: 25px;
  border-top: 1px solid var(--border);
  color: var(--text-faint);
  font-size: 11px;
}
.footer-meta,
.sync-status {
  display: flex;
  align-items: center;
  gap: 9px;
}
.footer-meta {
  gap: 22px;
}
.sync-status i {
  height: 5px;
  width: 5px;
  background: var(--official);
  border-radius: 50%;
}
.sync-status i.warning {
  background: var(--inferred);
}
@media (max-width: 850px) {
  .topbar-inner {
    gap: 28px;
  }
  .tabs {
    gap: 24px;
  }
}
@media (max-width: 640px) {
  .topbar-inner {
    flex-wrap: wrap;
    gap: 0;
    padding-top: 15px;
    min-height: auto;
  }
  .brand-name {
    font-size: 19px;
  }
  .brand-mark {
    width: 30px;
    height: 32px;
  }
  .topbar-actions {
    gap: 8px;
  }
  .tabs {
    order: 3;
    width: 100%;
    height: 48px;
    gap: 30px;
    margin-top: 5px;
  }
  .tabs a {
    font-size: 12px;
  }
  .share-button {
    border: 0;
    width: 30px;
  }
  .footer {
    align-items: flex-start;
    flex-direction: column;
    gap: 10px;
    padding-block: 20px;
  }
}
</style>
