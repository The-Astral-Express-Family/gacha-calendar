// Minimal history router: the backend serves the SPA for every non-asset
// path, so plain pushState navigation is enough for two routes.
import { reactive } from 'vue'

export type RouteName = 'dashboard' | 'posts'

const routes: Array<{ path: string; name: RouteName }> = [
  { path: '/', name: 'dashboard' },
  { path: '/posts', name: 'posts' },
]

function currentName(): RouteName {
  const path = window.location.pathname.replace(/\/+$/, '') || '/'
  return routes.find((route) => route.path === path)?.name ?? 'dashboard'
}

export const routeState = reactive<{ name: RouteName }>({ name: currentName() })

export function navigate(name: RouteName) {
  const route = routes.find((item) => item.name === name)
  if (!route || routeState.name === name) return
  routeState.name = name
  window.history.pushState(null, '', route.path)
  window.scrollTo({ top: 0 })
}

window.addEventListener('popstate', () => {
  routeState.name = currentName()
})
