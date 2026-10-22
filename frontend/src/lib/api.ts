// Typed client for the gacha-calendar API. All timestamps arrive as UTC
// RFC3339 strings with unix seconds alongside; conversion is presentation.

export interface TimeMeta {
  utc: string
  unix_seconds: number
  duration_seconds?: number
  until_seconds?: number
}

export interface TimeOnly {
  utc: string
}

export interface Prediction {
  id: number
  game_id: string
  game_name: string
  version: string
  version_name?: string
  update?: TimeMeta
  preview?: TimeMeta
  update_confidence: string
  preview_confidence: string
  update_evidence: string
  preview_evidence: string
  is_phase_update: boolean
  announcement_id?: number
  announcement_url?: string
  generated_at: TimeOnly
  active: boolean
}

export interface Game {
  id: string
  name: string
  bilibili_space: string
  cycle_days: number
  enabled: boolean
  status: string
  last_refresh_at?: TimeOnly
  last_error?: string
  cover?: string
  preview_cover?: string
  fallback: string
  calibrated: boolean
  version_count: number
  prediction?: Prediction
}

export interface Post {
  id: number
  game_id: string
  game_name: string
  source_type: 'article' | 'dynamic' | 'video' | string
  external_id: string
  url: string
  title: string
  summary: string
  content?: string
  author: string
  publish_time: TimeOnly
  fetched_at: TimeOnly
  cover_url?: string
  cover_path?: string
  is_update_notice: boolean
}

export interface Version {
  id: number
  game_id: string
  game_name: string
  version: string
  version_name: string
  update?: TimeMeta
  version_end?: TimeOnly
  preview?: TimeMeta
  announcement_id?: number
}

export interface Pagination {
  page: number
  page_size: number
  total: number
  total_pages: number
}

export interface ListResponse<T> {
  data: T[]
  pagination: Pagination
}

export interface Meta {
  service: string
  version: string
  server_time: string
  timezone: {
    business: string
    utc_offset: string
    note: string
  }
  refresh: {
    interval_seconds: number
    running: boolean
    last_cycle_at?: string
    last_cycle_ok: number
    last_cycle_all: number
    last_error: string
    next_run_at?: string
    scheduler?: string
  }
  bootstrap?: {
    state: string
    running: boolean
    finished_at?: string
  }
}

async function request<T>(path: string): Promise<T> {
  const response = await fetch(path, { headers: { Accept: 'application/json' } })
  if (!response.ok) {
    let message = `HTTP ${response.status}`
    try {
      const body = await response.json()
      if (body?.error?.message) message = body.error.message
    } catch {
      /* keep HTTP status text */
    }
    throw new Error(message)
  }
  return response.json() as Promise<T>
}

export interface PredictionQuery {
  page?: number
  pageSize?: number
  game?: string[]
  from?: string
  to?: string
  on?: 'any' | 'update' | 'preview'
  active?: boolean
  upcoming?: boolean
  sort?: 'update_start' | 'preview_start' | 'generated_at' | 'created_at'
  order?: 'asc' | 'desc'
}

function appendList(params: URLSearchParams, query: PredictionQuery | PostQuery | VersionQuery) {
  if (query.page) params.set('page', String(query.page))
  if (query.pageSize) params.set('page_size', String(query.pageSize))
  if (query.game?.length) params.set('games', query.game.join(','))
  if (query.from) params.set('from', query.from)
  if (query.to) params.set('to', query.to)
  if (query.order) params.set('order', query.order)
}

export function fetchPredictions(query: PredictionQuery = {}): Promise<ListResponse<Prediction>> {
  const params = new URLSearchParams()
  appendList(params, query)
  if (query.on) params.set('on', query.on)
  if (query.active !== undefined) params.set('active', query.active ? '1' : '0')
  if (query.upcoming) params.set('upcoming', '1')
  if (query.sort) params.set('sort', query.sort)
  const suffix = params.toString()
  return request<ListResponse<Prediction>>(`/api/predictions${suffix ? `?${suffix}` : ''}`)
}

export interface PostQuery {
  page?: number
  pageSize?: number
  game?: string[]
  from?: string
  to?: string
  type?: 'article' | 'dynamic' | 'video'
  q?: string
  isUpdateNotice?: boolean
  sort?: 'publish_time' | 'fetched_at' | 'created_at'
  order?: 'asc' | 'desc'
}

export function fetchPosts(query: PostQuery = {}): Promise<ListResponse<Post>> {
  const params = new URLSearchParams()
  appendList(params, query)
  if (query.type) params.set('type', query.type)
  if (query.q) params.set('q', query.q)
  if (query.isUpdateNotice !== undefined) params.set('is_update_notice', query.isUpdateNotice ? '1' : '0')
  if (query.sort) params.set('sort', query.sort)
  const suffix = params.toString()
  return request<ListResponse<Post>>(`/api/posts${suffix ? `?${suffix}` : ''}`)
}

export function fetchPost(id: number | string): Promise<{ data: Post }> {
  return request<{ data: Post }>(`/api/posts/${id}`)
}

export interface VersionQuery {
  page?: number
  pageSize?: number
  game?: string[]
  from?: string
  to?: string
  order?: 'asc' | 'desc'
}

export function fetchVersions(query: VersionQuery = {}): Promise<ListResponse<Version>> {
  const params = new URLSearchParams()
  appendList(params, query)
  const suffix = params.toString()
  return request<ListResponse<Version>>(`/api/versions${suffix ? `?${suffix}` : ''}`)
}

export function fetchGames(): Promise<{ data: Game[] }> {
  return request<{ data: Game[] }>('/api/games')
}

export function fetchMeta(): Promise<Meta> {
  return request<Meta>('/api/meta')
}
