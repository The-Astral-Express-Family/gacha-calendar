// Display-timezone state: auto-detect the browser IANA zone, allow manual
// switching (local / national-server / UTC / arbitrary IANA), persist in
// localStorage and accept ?tz= overrides for shareable links.
import { reactive, computed } from 'vue'

import type { TzValue } from '@/lib/time'
import { BUSINESS_TZ, detectLocalZone, resolveZone, zoneLabel, offsetLabel } from '@/lib/time'

const STORAGE_KEY = 'gacha-calendar.tz'

function initial(): TzValue {
  const param = new URLSearchParams(window.location.search).get('tz')
  if (param) {
    if (param === 'local' || param === 'shanghai' || param === 'utc') return param
    try {
      new Intl.DateTimeFormat('zh-CN', { timeZone: param })
      return param
    } catch { /* invalid zone in link, fall through */ }
  }
  const stored = window.localStorage.getItem(STORAGE_KEY)
  if (stored === 'local' || stored === 'shanghai' || stored === 'utc') return stored
  if (stored) {
    try {
      new Intl.DateTimeFormat('zh-CN', { timeZone: stored })
      return stored
    } catch { /* ignore bad stored value */ }
  }
  return 'local'
}

export const tzState = reactive({
  value: initial() as TzValue,
})

export const tzZone = computed(() => resolveZone(tzState.value))
export const tzZoneLabel = computed(() => zoneLabel(tzState.value))
export const tzOffset = computed(() => offsetLabel(tzZone.value))

export function setTz(value: TzValue) {
  tzState.value = value
  window.localStorage.setItem(STORAGE_KEY, value)
  const url = new URL(window.location.href)
  url.searchParams.set('tz', value)
  window.history.replaceState(null, '', url.toString())
}

/** Options for the switcher: local, business (CN), UTC, plus common extras. */
export function tzOptions(): Array<{ value: TzValue; label: string }> {
  const local = detectLocalZone()
  const options: Array<{ value: TzValue; label: string }> = [
    { value: 'local', label: `本地时间（${local}）` },
    { value: 'shanghai', label: '国服时间（Asia/Shanghai, UTC+8）' },
    { value: 'utc', label: 'UTC' },
  ]
  if (local !== BUSINESS_TZ) {
    options.push({ value: BUSINESS_TZ, label: `Asia/Shanghai` })
  }
  for (const extra of ['Asia/Tokyo', 'America/Los_Angeles', 'Europe/London']) {
    if (extra !== local && extra !== BUSINESS_TZ) {
      options.push({ value: extra, label: extra })
    }
  }
  return options
}
