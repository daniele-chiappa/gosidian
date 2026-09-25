import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

/** Build a `/?w=&f=` redirect that opens a single window for a cold deep-link.
 *  In-app navigation never goes through here — it calls the windows store
 *  directly — so these redirects only fire for address-bar / external links. */
function singleWindow(token: string) {
  return { path: '/', query: { w: token, f: token } }
}
function pathParam(to: { params: Record<string, unknown> }): string {
  const raw = to.params.path
  return Array.isArray(raw) ? raw.join('/') : String(raw ?? '')
}

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { requiresAuth: false, layout: 'bare' },
  },
  {
    // OAuth consent (IMP-092): reached by a 302 from /oauth/authorize; the
    // guard below routes through /login?next= when there is no session.
    path: '/oauth/consent',
    name: 'oauth-consent',
    component: () => import('@/views/OAuthConsentView.vue'),
    meta: { requiresAuth: true, layout: 'bare' },
  },
  {
    path: '/',
    name: 'home',
    component: () => import('@/components/layout/AppShell.vue'),
    meta: { requiresAuth: true },
  },

  // ── Legacy routes → open the matching window in the plancia ──────────────
  // Order matters: the /edit and /history suffixes must precede the bare note.
  {
    path: '/notes/:path(.*)/edit',
    redirect: (to) => singleWindow('edit:' + encodeURIComponent(pathParam(to))),
  },
  {
    path: '/notes/:path(.*)/history',
    redirect: (to) => singleWindow('history:' + encodeURIComponent(pathParam(to))),
  },
  {
    path: '/notes/:path(.*)',
    redirect: (to) => singleWindow('note:' + encodeURIComponent(pathParam(to))),
  },
  { path: '/search', redirect: () => singleWindow('search') },
  { path: '/projects', redirect: () => singleWindow('projects') },
  {
    path: '/tags/:tag',
    redirect: (to) => singleWindow('tags:' + encodeURIComponent(String(to.params.tag ?? ''))),
  },
  { path: '/tags', redirect: () => singleWindow('tags') },
  { path: '/graph', redirect: () => singleWindow('graph') },
  { path: '/settings', redirect: () => singleWindow('settings') },
  { path: '/trash', redirect: () => singleWindow('trash') },
  {
    path: '/admin/:section(.*)?',
    redirect: (to) => {
      const s = Array.isArray(to.params.section)
        ? to.params.section.join('/')
        : String(to.params.section ?? '')
      return singleWindow(s ? 'admin:' + encodeURIComponent(s) : 'admin')
    },
  },

  { path: '/:pathMatch(.*)*', redirect: () => ({ path: '/' }) },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to, _from) => {
  const auth = useAuthStore()
  const requires = to.matched.some((r) => r.meta.requiresAuth !== false)

  if (requires && !auth.isAuthenticated && !auth.isAnonymous) {
    return {
      name: 'login',
      query: to.fullPath !== '/' ? { next: to.fullPath } : {},
    }
  }
  if (to.name === 'login' && auth.isAuthenticated) {
    return { name: 'home' }
  }
  return true
})
