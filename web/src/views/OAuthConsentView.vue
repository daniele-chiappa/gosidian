<script setup lang="ts">
/**
 * OAuth consent screen (IMP-092). The authorization endpoint parks the
 * client's request server-side and redirects the browser here with ?req=;
 * the router guard sends an unauthenticated user through /login first and
 * back. On a decision the server answers with the client's redirect URL
 * (carrying the code or error=access_denied) and we leave the SPA.
 */
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import {
  getOAuthRequest,
  approveOAuthRequest,
  denyOAuthRequest,
  type OAuthConsent,
} from '@/api/oauth'

const { t } = useI18n()
const route = useRoute()
const auth = useAuthStore()

const requestId = computed(() => (typeof route.query.req === 'string' ? route.query.req : ''))
const consent = ref<OAuthConsent | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const busy = ref(false)
const redirecting = ref(false)

/** Project names the user keeps ticked; starts with everything visible. */
const selected = ref<Set<string>>(new Set())
const grantWrite = ref(false)

const writeRequested = computed(() => consent.value?.scopes.includes('write') ?? false)
const allSelected = computed(
  () => !!consent.value && consent.value.projects.every((p) => selected.value.has(p.name)),
)
const expiresAt = computed(() =>
  consent.value
    ? new Date(consent.value.expires_at).toLocaleTimeString([], {
        hour: '2-digit',
        minute: '2-digit',
      })
    : '',
)

onMounted(async () => {
  if (!requestId.value) {
    error.value = t('oauth.error_missing')
    loading.value = false
    return
  }
  try {
    const c = await getOAuthRequest(requestId.value)
    consent.value = c
    selected.value = new Set(c.projects.map((p) => p.name))
    grantWrite.value = c.can_write && c.scopes.includes('write')
  } catch (e: unknown) {
    const status = (e as { response?: { status?: number } })?.response?.status
    error.value = status === 404 ? t('oauth.error_missing') : t('oauth.error_generic')
  } finally {
    loading.value = false
  }
})

function toggle(name: string) {
  const next = new Set(selected.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  selected.value = next
}

function toggleAll() {
  if (!consent.value) return
  selected.value = allSelected.value
    ? new Set()
    : new Set(consent.value.projects.map((p) => p.name))
}

function leaveTo(url: string) {
  redirecting.value = true
  window.location.assign(url)
}

async function approve() {
  if (!consent.value || busy.value) return
  if (selected.value.size === 0) {
    error.value = t('oauth.none_selected')
    return
  }
  busy.value = true
  error.value = null
  try {
    // An owner keeping every project ticked gets an unscoped grant, which
    // also covers projects created later — that is what "all" means.
    const projects = consent.value.is_owner && allSelected.value ? [] : Array.from(selected.value)
    const scopes = ['read']
    if (grantWrite.value && consent.value.can_write) scopes.push('write')
    leaveTo(await approveOAuthRequest(consent.value.request_id, { projects, scopes }))
  } catch {
    error.value = t('oauth.error_generic')
    busy.value = false
  }
}

async function deny() {
  if (!consent.value || busy.value) return
  busy.value = true
  try {
    leaveTo(await denyOAuthRequest(consent.value.request_id))
  } catch {
    error.value = t('oauth.error_generic')
    busy.value = false
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-bg text-text px-4 py-8">
    <div class="w-full max-w-lg">
      <div class="mb-6 text-center">
        <h1 class="text-2xl font-semibold">gosidian</h1>
        <p v-if="auth.user" class="text-sm text-text-muted">
          {{ t('oauth.signed_in_as', { user: auth.user.username }) }}
        </p>
      </div>

      <div class="rounded-lg bg-surface p-5 shadow ring-1 ring-border space-y-4">
        <p v-if="loading" class="text-sm text-text-muted">
          {{ t('common.loading') }}
        </p>

        <template v-else-if="consent">
          <div>
            <h2 class="text-lg font-medium">
              {{ t('oauth.title', { client: consent.client_name }) }}
            </h2>
            <p class="text-sm text-text-muted">
              {{ t('oauth.subtitle') }}
            </p>
            <p class="mt-2 text-xs text-text-muted break-all">
              {{ t('oauth.redirect_to') }}
              <code class="rounded bg-bg-elevated px-1">{{ consent.redirect_host }}</code>
            </p>
            <p v-if="consent.client_cimd" class="mt-1 text-xs text-text-muted break-all">
              {{ t('oauth.client_id') }}
              <code class="rounded bg-bg-elevated px-1">{{ consent.client_id }}</code>
            </p>
          </div>

          <p
            v-if="consent.loopback_only"
            class="rounded border border-warning/60 bg-warning/10 p-3 text-sm"
            role="alert"
          >
            {{ t('oauth.loopback_warning') }}
          </p>

          <fieldset class="space-y-2">
            <legend class="text-sm font-medium">
              {{ t('oauth.projects') }}
            </legend>
            <label v-if="consent.projects.length > 1" class="flex items-center gap-2 text-sm">
              <input type="checkbox" :checked="allSelected" @change="toggleAll" />
              <span>{{ consent.is_owner ? t('oauth.all_projects') : t('oauth.select_all') }}</span>
            </label>
            <p v-if="consent.projects.length === 0" class="text-sm text-danger">
              {{ t('oauth.no_projects') }}
            </p>
            <ul class="max-h-56 overflow-y-auto space-y-1 pl-1">
              <li v-for="p in consent.projects" :key="p.name">
                <label class="flex items-center gap-2 text-sm">
                  <input type="checkbox" :checked="selected.has(p.name)" @change="toggle(p.name)" />
                  <span class="font-mono">{{ p.name }}</span>
                  <span class="text-xs text-text-muted">
                    {{ t('oauth.note_count', { n: p.note_count }) }}
                    <span v-if="p.public"> · {{ t('oauth.public') }}</span>
                  </span>
                </label>
              </li>
            </ul>
          </fieldset>

          <fieldset class="space-y-2">
            <legend class="text-sm font-medium">
              {{ t('oauth.scopes') }}
            </legend>
            <label class="flex items-center gap-2 text-sm">
              <input type="checkbox" checked disabled />
              <span>{{ t('oauth.scope_read') }}</span>
            </label>
            <label v-if="writeRequested" class="flex items-center gap-2 text-sm">
              <input v-model="grantWrite" type="checkbox" :disabled="!consent.can_write" />
              <span>{{ t('oauth.scope_write') }}</span>
              <span v-if="!consent.can_write" class="text-xs text-text-muted">
                — {{ t('oauth.write_unavailable') }}
              </span>
            </label>
          </fieldset>

          <p v-if="expiresAt" class="text-xs text-text-muted">
            {{ t('oauth.expires', { time: expiresAt }) }}
          </p>

          <p v-if="error" class="text-sm text-danger">
            {{ error }}
          </p>

          <div class="flex gap-3 pt-2">
            <button
              type="button"
              :disabled="busy || redirecting || consent.projects.length === 0"
              class="flex-1 rounded bg-accent text-accent-fg py-2 font-medium hover:bg-accent-hover disabled:opacity-60"
              @click="approve"
            >
              {{
                redirecting
                  ? t('oauth.redirecting')
                  : busy
                    ? t('oauth.working')
                    : t('oauth.approve')
              }}
            </button>
            <button
              type="button"
              :disabled="busy || redirecting"
              class="flex-1 rounded border border-border py-2 font-medium hover:bg-bg-elevated disabled:opacity-60"
              @click="deny"
            >
              {{ t('oauth.deny') }}
            </button>
          </div>
        </template>

        <p v-else class="text-sm text-danger">
          {{ error }}
        </p>
      </div>
    </div>
  </div>
</template>
