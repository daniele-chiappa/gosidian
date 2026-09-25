<script setup lang="ts">
/**
 * MyTokens — self-service MCP tokens for the signed-in account (IMP-101
 * phase 3). A token never outruns the account: the server narrows it on
 * every request to what the account may currently read and write. Inherit
 * follows the account's live access; custom pins a subset of the projects
 * visible now.
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { listMyTokens, createMyToken, revokeMyToken, type TokenMode } from '@/api/me'
import type { MCPToken, MCPTokenCreated } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import { useAccessStore } from '@/stores/access'

const auth = useAuthStore()
const access = useAccessStore()
const tokens = ref<MCPToken[]>([])
const loading = ref(false)
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
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load'
  } finally {
    loading.value = false
  }
}

async function create() {
  if (!draft.name.trim()) return
  if (draft.mode === 'custom' && draft.projects.length === 0) {
    error.value = 'Pick at least one project for a custom token.'
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
    })
    draft.name = ''
    draft.projects = []
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Create failed'
  } finally {
    busy.value = false
  }
}

async function revoke(t: MCPToken) {
  if (!confirm(`Revoke token "${t.name}"? Agents using it are locked out immediately.`)) return
  try {
    await revokeMyToken(t.id)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Revoke failed'
  }
}

function scopeLabel(t: MCPToken): string {
  const proj = t.projects && t.projects.length ? t.projects.join(', ') : 'inherit (follows your access)'
  return `${proj} · ${t.scopes.join('+')}${t.tool_profile ? ' · ' + t.tool_profile : ''}`
}

onMounted(() => {
  void load()
  if (!access.loaded) void access.load()
})
</script>

<template>
  <div class="space-y-3">
    <p class="text-xs text-text-muted">
      Tokens for MCP clients. A token never exceeds your own access: on every request it is
      narrowed to the projects you can read and write at that moment. <em>Inherit</em> follows
      your access as it changes; <em>custom</em> pins a subset of the projects you see now.
      OAuth logins (claude.ai, Claude Code) appear here too and can be revoked.
    </p>

    <div v-if="fresh" class="rounded border border-success bg-success/10 p-3 space-y-2">
      <p class="text-sm font-semibold text-success">Token created — copy now, this is the only time it is shown.</p>
      <code class="block bg-bg-elevated rounded px-3 py-2 font-mono text-sm break-all select-all">{{ fresh.token }}</code>
      <p class="text-xs text-text-muted">{{ fresh.usage_hint }}</p>
      <button type="button" class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover" @click="fresh = null">Dismiss</button>
    </div>

    <form class="grid gap-2 sm:grid-cols-2" @submit.prevent="create">
      <label class="text-sm sm:col-span-2">
        <span class="text-text-muted text-xs">Name</span>
        <input
          v-model.trim="draft.name"
          type="text"
          placeholder="e.g. laptop-claude"
          required
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
        />
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">Projects</span>
        <select v-model="draft.mode" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2">
          <option value="inherit">Inherit — everything I can see, now and later</option>
          <option value="custom">Custom — only the projects I pick</option>
        </select>
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">Scope</span>
        <select
          :value="draft.write && canWrite ? 'rw' : 'r'"
          :disabled="!canWrite"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2 disabled:opacity-60"
          @change="draft.write = ($event.target as HTMLSelectElement).value === 'rw'"
        >
          <option value="r">read only</option>
          <option value="rw">read + write (where I may write)</option>
        </select>
      </label>
      <label v-if="draft.mode === 'custom'" class="text-sm sm:col-span-2">
        <span class="text-text-muted text-xs">Pick projects (Ctrl/Cmd-click for several)</span>
        <select v-model="draft.projects" multiple size="4" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-1">
          <option v-for="p in visibleProjects" :key="p" :value="p">{{ p }}</option>
        </select>
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">Tool profile</span>
        <select v-model="draft.profile" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2">
          <option value="">full</option>
          <option value="core">core (worker subset)</option>
        </select>
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">Expires in days (0 = never)</span>
        <input v-model.number="draft.ttlDays" type="number" min="0" class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2" />
      </label>
      <div class="sm:col-span-2">
        <button
          type="submit"
          :disabled="busy || !draft.name"
          class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        >+ Create token</button>
      </div>
    </form>

    <p v-if="loading" class="text-text-muted text-sm">Loading…</p>
    <p v-if="error" class="text-danger text-sm">{{ error }}</p>

    <ul v-if="!loading" class="space-y-1">
      <li
        v-for="t in tokens"
        :key="t.id"
        class="flex items-center gap-2 text-sm rounded border border-border bg-surface px-3 py-2"
      >
        <span class="font-medium">{{ t.name }}</span>
        <span v-if="t.kind === 'oauth'" class="text-[10px] uppercase px-1.5 py-0.5 rounded border border-border text-text-muted">oauth</span>
        <span class="flex-1 truncate text-xs text-text-muted">{{ scopeLabel(t) }}</span>
        <span class="font-mono text-xs" :class="t.expired ? 'text-warning' : 'text-text-muted'">{{ t.expires_at || 'no expiry' }}</span>
        <button type="button" class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover" @click="revoke(t)">Revoke</button>
      </li>
      <li v-if="tokens.length === 0" class="text-xs text-text-muted">No tokens yet.</li>
    </ul>
  </div>
</template>
