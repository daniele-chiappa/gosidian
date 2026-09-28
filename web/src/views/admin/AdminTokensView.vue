<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import {
  listMCPTokens,
  createMCPToken,
  revokeMCPToken,
  setMCPTokenOptIn,
  listUsers,
  type AdminUser,
  type MCPToken,
  type MCPTokenCreated,
} from '@/api/admin'

const tokens = ref<MCPToken[]>([])
const users = ref<Map<string, AdminUser>>(new Map())
// false when the user list could not be read: labels then avoid guessing.
const usersLoaded = ref(false)
const loading = ref(false)
const error = ref<string | null>(null)
const fresh = ref<MCPTokenCreated | null>(null)

const draft = reactive<{ name: string; project: string; scopes: string; ttl_ms: number }>({
  name: '',
  project: '',
  scopes: 'read,write',
  ttl_ms: 0,
})

async function load() {
  loading.value = true
  error.value = null
  try {
    // The owner column is a convenience: a failed user list must not hide
    // the tokens.
    const [list, accounts] = await Promise.all([
      listMCPTokens(),
      listUsers().catch(() => null),
    ])
    tokens.value = list
    usersLoaded.value = accounts !== null
    users.value = new Map((accounts ?? []).map((u) => [u.id, u]))
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load'
  } finally {
    loading.value = false
  }
}

async function create() {
  if (!draft.name.trim()) return
  try {
    fresh.value = await createMCPToken({
      name: draft.name.trim(),
      project: draft.project.trim() || undefined,
      scopes: draft.scopes.split(',').map((s) => s.trim()).filter(Boolean),
      ttl_ms: draft.ttl_ms || undefined,
    })
    draft.name = ''
    draft.project = ''
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Create failed'
  }
}

async function revoke(t: MCPToken) {
  if (!confirm(`Revoke token "${t.name}"? Agents using it will be locked out immediately.`)) return
  try {
    await revokeMCPToken(t.id)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Revoke failed'
  }
}

async function toggleOptIn(t: MCPToken) {
  try {
    const updated = await setMCPTokenOptIn(t.id, !t.self_improve_opt_in)
    t.self_improve_opt_in = updated.self_improve_opt_in
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Update failed'
  }
}

// An empty project list is admin only for a CLI token or one owned by the
// owner account; any other account's token follows that account's live
// access (BUG-062).
function scopeLabel(t: MCPToken): string {
  if (t.projects?.length) return t.projects.join(', ')
  if (t.project) return t.project
  if (!t.owner_user_id || users.value.get(t.owner_user_id)?.role === 'owner') return '(admin)'
  return usersLoaded.value ? '(inherit)' : '—'
}

function ownerLabel(t: MCPToken): string {
  if (!t.owner_user_id) return '— (CLI)'
  const u = users.value.get(t.owner_user_id)
  if (!u) return usersLoaded.value ? `${t.owner_user_id} (missing)` : t.owner_user_id
  return u.disabled_at ? `${u.username} (disabled)` : u.username
}

function dismissFresh() {
  fresh.value = null
}

onMounted(load)
</script>

<template>
  <div v-if="fresh" class="rounded border border-success bg-success/10 p-4 mb-6 space-y-2">
    <p class="text-sm font-semibold text-success">Token created — copy now, this is the only time it is shown.</p>
    <code class="block bg-bg-elevated rounded px-3 py-2 font-mono text-sm break-all select-all">{{ fresh.token }}</code>
    <p class="text-xs text-text-muted">{{ fresh.usage_hint }}</p>
    <button
      type="button"
      class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover"
      @click="dismissFresh"
    >Dismiss</button>
  </div>

  <form
    class="grid grid-cols-1 md:grid-cols-5 gap-3 mb-6"
    @submit.prevent="create"
  >
    <input
      v-model.trim="draft.name"
      type="text"
      placeholder="name (e.g. agent-alpha)"
      class="rounded bg-bg-elevated border border-border px-3 py-2 md:col-span-2"
    />
    <input
      v-model.trim="draft.project"
      type="text"
      placeholder="project (optional)"
      class="rounded bg-bg-elevated border border-border px-3 py-2"
    />
    <input
      v-model.trim="draft.scopes"
      type="text"
      placeholder="scopes csv"
      class="rounded bg-bg-elevated border border-border px-3 py-2"
    />
    <button
      type="submit"
      class="px-3 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover"
    >Create</button>
  </form>

  <p v-if="loading" class="text-text-muted">Loading…</p>
  <p v-else-if="error" class="text-danger">{{ error }}</p>

  <table v-else class="w-full text-sm">
    <thead class="text-text-muted text-xs uppercase tracking-wide">
      <tr>
        <th class="text-left py-2 px-3">Name</th>
        <th class="text-left py-2 px-3">Owner</th>
        <th class="text-left py-2 px-3">Project</th>
        <th class="text-left py-2 px-3">Scopes</th>
        <th class="text-left py-2 px-3">Created</th>
        <th class="text-left py-2 px-3">Expires</th>
        <th class="text-left py-2 px-3">Self-improve</th>
        <th class="text-right py-2 px-3">Actions</th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="t in tokens"
        :key="t.id"
        class="border-t border-border"
      >
        <td class="py-2 px-3 font-medium">{{ t.name }}</td>
        <td class="py-2 px-3">{{ ownerLabel(t) }}</td>
        <td
          class="py-2 px-3"
          :title="scopeLabel(t) === '(inherit)' ? 'Follows the owning account\'s current project access' : undefined"
        >{{ scopeLabel(t) }}</td>
        <td class="py-2 px-3">
          <span
            v-for="s in t.scopes"
            :key="s"
            class="text-xs px-1.5 py-0.5 rounded border border-border mr-1"
          >{{ s }}</span>
        </td>
        <td class="py-2 px-3 font-mono text-xs">{{ t.created_at }}</td>
        <td class="py-2 px-3 font-mono text-xs">
          <span :class="t.expired ? 'text-warning' : ''">{{ t.expires_at || '—' }}</span>
        </td>
        <td class="py-2 px-3">
          <button
            type="button"
            class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover"
            :class="t.self_improve_opt_in ? 'text-success' : 'text-text-muted'"
            :title="t.self_improve_opt_in ? 'Opted in — click to withdraw' : 'Not opted in — click to enrol'"
            @click="toggleOptIn(t)"
          >{{ t.self_improve_opt_in ? 'opt-in ✓' : 'off' }}</button>
        </td>
        <td class="py-2 px-3 text-right">
          <button
            type="button"
            class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
            @click="revoke(t)"
          >Revoke</button>
        </td>
      </tr>
    </tbody>
  </table>
</template>
