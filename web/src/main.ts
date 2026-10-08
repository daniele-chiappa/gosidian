import { createApp } from 'vue'
import { createPinia } from 'pinia'
import piniaPersist from 'pinia-plugin-persistedstate'
import App from './App.vue'
import { router } from './router'
import { i18n } from './locales'
import { useAuthStore } from './stores/auth'
import { getVersion } from './api/version'
import './styles/tokens.css'
import './styles/tailwind.css'
import './styles/code.css'
import 'plancia/style.css'
import './styles/plancia-bridge.css'

const app = createApp(App)
const pinia = createPinia()
pinia.use(piniaPersist)
app.use(pinia)
app.use(router)
app.use(i18n)

// UI hydrate (theme + locale) runs inside App.vue setup() — calling
// useStore() here, before app.mount(), can race Pinia's active-
// instance setup and crash the bundle (blank page / no shell).

// Open-mode bootstrap (BUG-018): if we have no token, ask the server whether
// it runs read-only anonymous access. If so, establish a guest session so the
// router guard renders the shell instead of bouncing to /login. Best-effort —
// a failed /version (or open-mode off) falls through to the normal flow, and a
// stale persisted anonymous user is dropped. Pinia is installed, so passing it
// explicitly avoids the active-instance race the comment above warns about.
async function boot() {
  const auth = useAuthStore(pinia)
  if (!auth.token) {
    // Bounded: a slow or hanging /version must never block first paint.
    // On open_mode → guest session; otherwise (off, error, or timeout) drop
    // any stale persisted anonymous user and fall through to the login flow.
    const v = await Promise.race([
      getVersion().catch(() => null),
      new Promise<null>((resolve) => {
        window.setTimeout(() => resolve(null), 3000)
      }),
    ])
    if (v?.open_mode) auth.setAnonymous()
    else if (auth.user?.id === 'anonymous') auth.clear()
  }
  app.mount('#app')
}

void boot()
