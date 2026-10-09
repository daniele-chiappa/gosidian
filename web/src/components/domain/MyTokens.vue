<script setup lang="ts">
/**
 * MyTokens — self-service MCP tokens for the signed-in account (IMP-101
 * phase 3). A token never outruns the account: the server narrows it on
 * every request to what the account may currently read and write. Inherit
 * follows the account's live access; custom pins a subset of the projects
 * visible now.
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listMyTokens, createMyToken, revokeMyToken, type TokenMode } from '@/api/me'
import type { MCPToken, MCPTokenCreated } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import { useAccessStore } from '@/stores/access'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { formatDateTime } from '@/api/format'
import { confirmAction } from '@/composables/useConfirm'

const auth = useAuthStore()
const access = useAccessStore()
const { t: tr } = useI18n()
// A token outlives the session that mints it: creating one asks for the
// password (IMP-088).
const password = ref('')
const tokens = ref<MCPToken[]>([])
const loading = ref(false)
// Only the first load puts "Loading…" in place of the list: a reload after
// an action took the whole list down and back, focus and scroll with it.
const loaded = ref(false)
const error = ref<string | null>(null)
const fresh = ref<MCPTokenCreated | null>(null)
const busy = ref(false)

const draft = reactive<{ name: string; mode: TokenMode; projects: string[]; write: boolean; profile: '' | 'core'; ttlDays: number }>({
  name: '',
  mode: 'inherit',
  projects: [],
  write: true,
  profile: '',
  ttlDays: 0,
})

const canWrite = computed(() => auth.canWrite)
const visibleProjects = computed(() => access.list.map((p) => p.name))

async function load() {
  loading.value = true
  error.value = null
  try {
    tokens.value = await listMyTokens()
    loaded.value = true
  } catch (e) {
    error.value = errorText(e, tr, tr('my_tokens.load_failed'))
  } finally {
    loading.value = false
  }
}

async function create() {
  // A second click before the reply minted a second token, and the secret
  // of the first, shown once, was lost.
  if (busy.value || !draft.name.trim() || !password.value) return
  if (draft.mode === 'custom' && draft.projects.length === 0) {
    error.value = tr('my_tokens.pick_one')
    return
  }
  busy.value = true
  error.value = null
  try {
    fresh.value = await createMyToken({
      name: draft.name.trim(),
      mode: draft.mode,
      projects: draft.mode === 'custom' ? draft.projects : undefined,
      scopes: draft.write && canWrite.value ? ['read', 'write'] : ['read'],
      tool_profile: draft.profile || undefined,
      ttl_ms: draft.ttlDays > 0 ? draft.ttlDays * 24 * 3600 * 1000 : undefined,
      password: password.value,
    })
    draft.name = ''
    draft.projects = []
    await load()
  } catch (e) {
    error.value = errorText(e, tr, tr('my_tokens.create_failed'))
  } finally {
    password.value = ''
    busy.value = false
  }
}

async function revoke(tok: MCPToken) {
  if (!(await confirmAction(tr('my_tokens.confirm_revoke', { name: tok.name }), { confirmLabel: tr('common.revoke') }))) return
  try {
    await revokeMyToken(tok.id)
    await load()
  } catch (e) {
    error.value = errorText(e, tr, tr('my_tokens.revoke_failed'))
  }
}

function scopeLabel(tok: MCPToken): string {
  const proj = tok.projects && tok.projects.length ? tok.projects.join(', ') : tr('my_tokens.inherit_label')
  return `${proj} · ${tok.scopes.join('+')}${tok.tool_profile ? ' · ' + tok.tool_profile : ''}`
}

onMounted(() => {
  void load()
  if (!access.loaded) void access.load()
})
</script>

<template>
  <div class="space-y-3">
    <p class="text-xs text-text-muted">
      {{ tr('my_tokens.intro') }}
    </p>

    <div v-if="fresh" class="rounded border border-success bg-success/10 p-3 space-y-2">
      <p class="text-sm font-semibold text-success">{{ tr('my_tokens.created') }}</p>
      <code class="block bg-bg-elevated rounded px-3 py-2 font-mono text-sm break-all select-all">{{ fresh.token }}</code>
      <p class="text-xs text-text-muted">{{ fresh.usage_hint }}</p>
      <button type="button" class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover" @click="fresh = null">{{ tr('common.dismiss') }}</button>
    </div>

    <form class="grid grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-2" @submit.prevent="create">
      <label class="text-sm col-span-full">
        <span class="text-text-muted text-xs">{{ tr('my_tokens.name') }}</span>
        <input
          v-model.trim="draft.name"
          type="text"
          :placeholder="tr('my_tokens.name_placeholder')"
          required
          class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
        />
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">{{ tr('my_tokens.projects') }}</span>
        <select v-model="draft.mode" class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-2">
          <option value="inherit">{{ tr('my_tokens.inherit') }}</option>
          <option value="custom">{{ tr('my_tokens.custom') }}</option>
        </select>
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">{{ tr('my_tokens.scope') }}</span>
        <select
          :value="draft.write && canWrite ? 'rw' : 'r'"
          :disabled="!canWrite"
          class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-2 disabled:opacity-60"
          @change="draft.write = ($event.target as HTMLSelectElement).value === 'rw'"
        >
          <option value="r">{{ tr('my_tokens.read_only') }}</option>
          <option value="rw">{{ tr('my_tokens.read_write') }}</option>
        </select>
      </label>
      <label v-if="draft.mode === 'custom'" class="text-sm col-span-full">
        <span class="text-text-muted text-xs">{{ tr('my_tokens.pick') }}</span>
        <select v-model="draft.projects" multiple size="4" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-1">
          <option v-for="p in visibleProjects" :key="p" :value="p">{{ p }}</option>
        </select>
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">{{ tr('my_tokens.profile') }}</span>
        <select v-model="draft.profile" class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-2">
          <option value="">{{ tr('my_tokens.profile_full') }}</option>
          <option value="core">{{ tr('my_tokens.profile_core') }}</option>
        </select>
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">{{ tr('my_tokens.ttl') }}</span>
        <input v-model.number="draft.ttlDays" type="number" min="0" class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3" />
      </label>
      <label class="text-sm col-span-full">
        <span class="text-text-muted text-xs">{{ tr('password.confirm_action') }}</span>
        <input
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
          data-token-password
          class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
        />
      </label>
      <div class="col-span-full">
        <button
          type="submit"
          :disabled="busy || !draft.name || !password"
          class="h-control rounded bg-accent text-accent-fg px-3 text-sm hover:bg-accent-hover disabled:opacity-60"
        >{{ tr('my_tokens.create') }}</button>
      </div>
    </form>

    <p v-if="loading && !loaded" class="text-text-muted text-sm">{{ tr('common.loading') }}</p>
    <ErrorMessage v-if="error" :text="error" class="text-sm" />

    <ul v-if="loaded" class="space-y-1">
      <li
        v-for="tok in tokens"
        :key="tok.id"
        class="flex flex-wrap items-center gap-2 text-sm rounded border border-border bg-surface px-3 py-2"
      >
        <span class="font-medium">{{ tok.name }}</span>
        <span v-if="tok.kind === 'oauth'" class="text-[10px] uppercase px-1.5 py-0.5 rounded border border-border text-text-muted">oauth</span>
        <span class="min-w-0 flex-1 truncate text-xs text-text-muted">{{ scopeLabel(tok) }}</span>
        <span
          class="text-xs text-text-muted"
          data-last-used
          :title="tok.last_used_at ?? tr('my_tokens.never_used_hint')"
        >{{ tok.last_used_at ? tr('my_tokens.used', { when: formatDateTime(tok.last_used_at) }) : tr('my_tokens.never_used') }}</span>
        <span class="text-xs" :class="tok.expired ? 'text-warning' : 'text-text-muted'" :title="tok.expires_at || undefined">{{
          tok.expires_at ? tr('my_tokens.expires', { when: formatDateTime(tok.expires_at) }) : tr('my_tokens.no_expiry')
        }}</span>
        <button type="button" class="h-control-sm text-xs px-2 rounded text-danger hover:bg-surface-hover" @click="revoke(tok)">{{ tr('common.revoke') }}</button>
      </li>
      <li v-if="tokens.length === 0" class="text-xs text-text-muted">{{ tr('my_tokens.none') }}</li>
    </ul>
  </div>
</template>
