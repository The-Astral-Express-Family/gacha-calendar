// Timezone utilities. The API speaks UTC only; every wall-clock rendering
// goes through Intl with an explicit IANA timezone, never Date-local methods.

export const BUSINESS_TZ = 'Asia/Shanghai'
export const BUSINESS_TZ_LABEL = '国服时间'
export const BUSINESS_TZ_OFFSET = '+08:00'

export type TzValue = 'local' | 'shanghai' | 'utc' | string

export interface TzOption {
  value: TzValue
  label: string
  zone: string // resolved IANA zone ('' for UTC)
}

export function detectLocalZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function resolveZone(value: TzValue): string {
  if (value === 'utc') return 'UTC'
  if (value === 'shanghai') return BUSINESS_TZ
  if (value === 'local') return detectLocalZone()
  return value
}

export function offsetLabel(zone: string, at: Date = new Date()): string {
  try {
    // "GMT+8" / "GMT+5:30" via shortOffset; fallback to long name.
    const parts = new Intl.DateTimeFormat('zh-CN', {
      timeZone: zone,
      timeZoneName: 'shortOffset',
    }).formatToParts(at)
    const name = parts.find((part) => part.type === 'timeZoneName')?.value
    if (name && !name.includes('GMT')) return name
    return name ?? 'UTC'
  } catch {
    return 'UTC'
  }
}

export function zoneLabel(value: TzValue): string {
  if (value === 'utc') return 'UTC'
  if (value === 'shanghai') return `${BUSINESS_TZ_LABEL}（Asia/Shanghai）`
  const zone = resolveZone(value)
  if (value === 'local') return `本地（${zone}）`
  return zone
}

function formatter(zone: string, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat('zh-CN', { timeZone: zone, ...options })
}

/** "2026-11-11 06:00" in the given zone. */
export function formatDateTime(zone: string, unixSeconds: number): string {
  return formatter(zone, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(unixSeconds * 1000))
}

/** "11-11 周三 06:00" in the given zone. */
export function formatShort(zone: string, unixSeconds: number): string {
  return formatter(zone, {
    month: '2-digit',
    day: '2-digit',
    weekday: 'short',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(unixSeconds * 1000))
}

/** "2026-11-11" in the given zone. */
export function formatDate(zone: string, unixSeconds: number): string {
  return formatter(zone, { year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(unixSeconds * 1000))
}

/** "11-11" in the given zone. */
export function formatDateShort(zone: string, unixSeconds: number): string {
  return formatter(zone, { month: '2-digit', day: '2-digit' }).format(new Date(unixSeconds * 1000))
}

export function formatTime(zone: string, unixSeconds: number): string {
  return formatter(zone, { hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(unixSeconds * 1000))
}

export function formatWeekday(zone: string, unixSeconds: number): string {
  return formatter(zone, { weekday: 'short' }).format(new Date(unixSeconds * 1000))
}

/**
 * Day key "YYYY-MM-DD" of an instant in the given zone — the calendar
 * day-boundary primitive used for grouping and month grids.
 */
export function dayKey(zone: string, unixSeconds: number): string {
  return formatter(zone, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
    .format(new Date(unixSeconds * 1000))
    .replaceAll('/', '-')
    .replaceAll('年', '-')
    .replaceAll('月', '-')
    .replaceAll('日', '')
}

/** Inverse of dayKey: unix seconds of wall-clock midnight in the zone. */
export function zoneDayStart(zone: string, key: string): number {
  // Compute the offset of the zone at an approximate instant, then refine
  // once — DST-free zones (Shanghai/UTC and most locals) converge instantly.
  const [y, m, d] = key.split('-').map(Number)
  const guessUTC = Date.UTC(y, m - 1, d, 0, 0, 0) / 1000
  let offset = zoneOffsetSeconds(zone, new Date(guessUTC * 1000))
  let stamp = guessUTC - offset
  offset = zoneOffsetSeconds(zone, new Date(stamp * 1000))
  stamp = guessUTC - offset
  return stamp
}

export function zoneOffsetSeconds(zone: string, at: Date = new Date()): number {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).formatToParts(at)
    const get = (type: string) => Number(parts.find((part) => part.type === type)?.value)
    const asUTC = Date.UTC(get('year'), get('month') - 1, get('day'), get('hour') % 24, get('minute'), get('second'))
    return Math.round((asUTC - at.getTime()) / 1000)
  } catch {
    return 0
  }
}

/** Human relative time computed from UTC stamps ("3 天后", "2 小时前"). */
export function relativeTime(unixSeconds: number, nowUnix: number = Date.now() / 1000): string {
  const delta = unixSeconds - nowUnix
  const abs = Math.abs(delta)
  const minute = 60
  const hour = 60 * minute
  const day = 24 * hour
  let text: string
  if (abs < minute) text = '刚刚'
  else if (abs < hour) text = `${Math.round(abs / minute)} 分钟`
  else if (abs < day) text = `${Math.round(abs / hour)} 小时`
  else if (abs < 30 * day) text = `${Math.round(abs / day)} 天`
  else text = `${Math.round(abs / (30 * day))} 个月`
  if (text === '刚刚') return text
  return delta > 0 ? `${text}后` : `${text}前`
}

export function isToday(zone: string, unixSeconds: number, nowUnix: number = Date.now() / 1000): boolean {
  return dayKey(zone, unixSeconds) === dayKey(zone, nowUnix)
}

/** Convert a "YYYY-MM-DD" day (in `zone`) to a UTC RFC3339 left-closed bound. */
export function dayToRFC3339Start(zone: string, key: string): string {
  return new Date(zoneDayStart(zone, key) * 1000).toISOString().replace('.000', '')
}

/** Right-open bound: first second after the day. */
export function dayToRFC3339EndExclusive(zone: string, key: string): string {
  return new Date((zoneDayStart(zone, key) + 1) * 1000).toISOString().replace('.000', '')
}

/** RFC3339 with .000 trimmed, for query parameters. */
export function toRFC3339(date: Date): string {
  return date.toISOString().replace('.000', '')
}
