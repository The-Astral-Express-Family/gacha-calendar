// Per-game accent colors (kept in sync with the backend fallback visuals)
// and small formatting helpers shared by views.

export const GAME_COLORS: Record<string, string> = {
  genshin: '#4f83f0',
  hsr: '#a78bfa',
  zzz: '#f5a623',
  wuwa: '#22d3ee',
  endfield: '#84cc16',
  nte: '#f472b6',
}

export function gameColor(id: string): string {
  return GAME_COLORS[id] ?? '#818cf8'
}

// Short lane labels for the timeline gutter (full names don't fit on mobile).
export const SHORT_NAMES: Record<string, string> = {
  genshin: '原神',
  hsr: '星铁',
  zzz: '绝区零',
  wuwa: '鸣潮',
  endfield: '终末地',
  nte: '异环',
}

export function shortName(game: { id: string; name: string }): string {
  return SHORT_NAMES[game.id] ?? game.name
}

export const CONFIDENCE_LABELS: Record<string, string> = {
  official: '官方',
  inferred: '推算',
  agent: '校正',
  none: '无前瞻',
}

export function confidenceLabel(value?: string): string {
  if (!value) return '—'
  return CONFIDENCE_LABELS[value] ?? value
}

export function confidenceClass(value?: string): string {
  switch (value) {
    case 'official':
      return 'badge-official'
    case 'agent':
      return 'badge-agent'
    case 'none':
      return 'badge-none'
    default:
      return 'badge-inferred'
  }
}

export const SOURCE_TYPE_LABELS: Record<string, string> = {
  article: '专栏公告',
  dynamic: '官方动态',
  video: '官方视频',
  live: '直播间公告',
}

export function sourceTypeLabel(value: string): string {
  return SOURCE_TYPE_LABELS[value] ?? value
}
