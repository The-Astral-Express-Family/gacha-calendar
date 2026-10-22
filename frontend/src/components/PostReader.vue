<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import CoverImage from '@/components/CoverImage.vue'
import UiIcon from '@/components/UiIcon.vue'
import type { Post } from '@/lib/api'
import { sourceTypeLabel } from '@/lib/games'
import { postTitle } from '@/lib/posts'
import { tzZone } from '@/stores/tz'
import { formatDateTime } from '@/lib/time'

const props = defineProps<{ post: Post | null; loading: boolean; error: string; gameCover?: string }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'retry'): void }>()
const dialog = ref<HTMLDialogElement | null>(null)
let previousOverflow: string | undefined
const readerText = computed(() => {
  if (!props.post) return ''
  const body = (props.post.content || props.post.summary).trim()
  if (body === props.post.title.trim()) return ''
  const lines = body.split('\n')
  return lines[0]?.trim() === postTitle(props.post) ? lines.slice(1).join('\n').trim() : body
})

function unlockScroll() {
  if (previousOverflow !== undefined) {
    document.body.style.overflow = previousOverflow
    previousOverflow = undefined
  }
}
function onClose() {
  emit('close')
  unlockScroll()
}

watch(
  () => props.post,
  async (post) => {
    await nextTick()
    if (post && !dialog.value?.open) {
      previousOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      dialog.value?.showModal()
    } else if (!post && dialog.value?.open) {
      dialog.value.close()
      unlockScroll()
    }
  },
  { immediate: true }
)
onBeforeUnmount(unlockScroll)
</script>

<template>
  <Teleport to="body">
    <dialog
      ref="dialog"
      class="post-reader"
      aria-labelledby="reader-title"
      @close="onClose"
      @click="$event.target === $event.currentTarget && emit('close')"
    >
      <div v-if="post" class="reader-shell">
        <div class="reader-visual">
          <CoverImage
            :src="post.cover_path || post.cover_url"
            :secondary="gameCover"
            :fallback="`/fallback/${post.game_id}.svg`"
            :alt="post.game_name"
            eager
          />
          <div class="reader-shade" />
          <span class="reader-game">{{ post.game_name }}</span>
          <button class="reader-close icon-button" aria-label="关闭公告" autofocus @click="emit('close')">
            <UiIcon name="close" :size="20" />
          </button>
        </div>
        <div class="reader-body">
          <div class="reader-meta">
            <span>{{ sourceTypeLabel(post.source_type) }}</span
            ><time :datetime="post.publish_time.utc">{{
              formatDateTime(tzZone, Date.parse(post.publish_time.utc) / 1000)
            }}</time>
          </div>
          <h2 id="reader-title">{{ postTitle(post) }}</h2>
          <div v-if="loading" class="reader-loading" role="status">正在加载正文…</div>
          <div v-else-if="error" class="error-state" role="alert">
            <span>{{ error }}</span
            ><button @click="emit('retry')">重试</button>
          </div>
          <div v-else-if="readerText" class="reader-content">
            {{ readerText }}
          </div>
          <a :href="post.url" target="_blank" rel="noreferrer" class="reader-source"
            >官方原文<UiIcon name="arrow" :size="15"
          /></a>
        </div>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.post-reader {
  width: min(760px, calc(100% - 40px));
  max-height: calc(100dvh - 64px);
  padding: 0;
  border: 1px solid #4a5463;
  border-radius: 16px;
  background: var(--panel);
  color: var(--text);
  box-shadow: 0 32px 100px #0009;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.post-reader::backdrop {
  background: #070b12be;
  backdrop-filter: blur(7px);
}
.reader-visual {
  position: relative;
  aspect-ratio: 16 / 8;
}
.reader-visual > img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.reader-shade {
  position: absolute;
  inset: 0;
  background: linear-gradient(transparent 55%, #1b2028);
}
.reader-game {
  position: absolute;
  bottom: 20px;
  left: 32px;
  font-size: 14px;
  color: #fff;
}
.reader-close {
  position: absolute;
  right: 16px;
  top: 16px;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  border-color: #ffffff4d;
  background: #111a24b3;
  color: #fff;
  backdrop-filter: blur(12px);
}
.reader-body {
  padding: 8px 32px 32px;
}
.reader-meta {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
  font-size: 11px;
  color: var(--text-faint);
}
.reader-body h2 {
  font-size: 22px;
  line-height: 1.6;
  margin: 17px 0 24px;
  font-weight: 550;
  overflow-wrap: anywhere;
  white-space: pre-line;
}
.reader-content {
  color: var(--text-dim);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.95;
  font-size: 14px;
}
.reader-loading {
  color: var(--text-faint);
  padding-block: 20px;
}
.reader-source {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  padding: 10px 15px;
  background: var(--accent-soft);
  border: 1px solid #a9d5f530;
  border-radius: 7px;
  margin-top: 28px;
  font-size: 12px;
}
@media (max-width: 640px) {
  .post-reader {
    width: calc(100% - 24px);
    max-height: calc(100dvh - 32px);
    border-radius: 12px;
  }
  .reader-body {
    padding: 8px 22px 24px;
  }
  .reader-game {
    left: 22px;
    bottom: 15px;
    font-size: 12px;
  }
  .reader-body h2 {
    font-size: 18px;
  }
}
</style>
