<script setup lang="ts">
import { useI18n } from 'vue-i18n'
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
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'

const { t } = useI18n()

const tokens = ref<MCPToken[]>([])
const users = ref<Map<string, AdminUser>>(new Map())
// false when the user list could not be read: labels then avoid guessing.
const usersLoaded = ref(false)
const loading = ref(false)
// Only the first load puts "Loading…" in place of the list: a reload after
// an action took the whole list down and back, focus and scroll with it.
const loaded = ref(false)
const error = ref<string | null>(null)
const fresh = ref<MCPTokenCreated | null>(null)
// A second click before the reply minted a second token, and the secret of
// the first, shown once, was lost.
const creating = ref(false)

const draft = reactive<{ name: string; project: string; scopes: string; ttl_ms: number; password: string }>({
  name: '',
  project: '',
  scopes: 'read,write',
  ttl_ms: 0,
  // A token outlives the session that mints it: the owner's password
  // confirms it (IMP-088).
  password: '',
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
    loaded.value = true
  } catch (e) {
    error.value = errorText(e, t, t('my_tokens.load_failed'))
  } finally {
    loading.value = false
  }
}

async function create() {
  if (creating.value || !draft.name.trim() || !draft.password) return
  creating.value = true
  try {
    fresh.value = await createMCPToken({
      name: draft.name.trim(),
      project: draft.project.trim() || undefined,
      scopes: draft.scopes.split(',').map((s) => s.trim()).filter(Boolean),
      ttl_ms: draft.ttl_ms || undefined,
      password: draft.password,
    })
    draft.name = ''
    draft.project = ''
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('my_tokens.create_failed'))
  } finally {
    creating.value = false
    draft.password = ''
  }
}

async function revoke(tok: MCPToken) {
  if (!confirm(t('my_tokens.confirm_revoke', { name: tok.name }))) return
  try {
    await revokeMCPToken(tok.id)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('my_tokens.revoke_failed'))
  }
}

async function toggleOptIn(tok: MCPToken) {
  try {
    const updated = await setMCPTokenOptIn(tok.id, !tok.self_improve_opt_in)
    tok.self_improve_opt_in = updated.self_improve_opt_in
  } catch (e) {
    error.value = errorText(e, t, t('admin.tokens.update_failed'))
  }
}

// An empty project list is admin only for a CLI token or one owned by the
// owner account; any other account's token follows that account's live
// access (BUG-062).
function inherits(tok: MCPToken): boolean {
  return !tok.projects?.length && !tok.project && !!tok.owner_user_id && usersLoaded.value &&
    users.value.get(tok.owner_user_id)?.role !== 'owner'
}
function scopeLabel(tok: MCPToken): string {
  if (tok.projects?.length) return tok.projects.join(', ')
  if (tok.project) return tok.project
  if (!tok.owner_user_id || users.value.get(tok.owner_user_id)?.role === 'owner') return t('admin.tokens.scope_admin')
  return usersLoaded.value ? t('admin.tokens.scope_inherit') : '—'
}

function ownerLabel(tok: MCPToken): string {
  if (!tok.owner_user_id) return t('admin.tokens.owner_cli')
  const u = users.value.get(tok.owner_user_id)
  if (!u) return usersLoaded.value ? t('admin.tokens.owner_missing', { id: tok.owner_user_id }) : tok.owner_user_id
  return u.disabled_at ? t('admin.tokens.owner_disabled', { name: u.username }) : u.username
}

function dismissFresh() {
  fresh.value = null
}

onMounted(load)
</script>

<template>
  <div v-if="fresh" class="rounded border border-success bg-success/10 p-4 mb-6 space-y-2">
    <p class="text-sm font-semibold text-success">{{ t('my_tokens.created') }}</p>
    <code class="block bg-bg-elevated rounded px-3 py-2 font-mono text-sm break-all select-all">{{ fresh.token }}</code>
    <p class="text-xs text-text-muted">{{ fresh.usage_hint }}</p>
    <button
      type="button"
      class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover"
      @click="dismissFresh"
    >{{ t('common.dismiss') }}</button>
  </div>

  <form
    class="grid grid-cols-[repeat(auto-fill,minmax(11rem,1fr))] gap-3 mb-6"
    @submit.prevent="create"
  >
    <input
      v-model.trim="draft.name"
      type="text"
      :placeholder="t('admin.tokens.name_placeholder')"
      class="rounded bg-bg-elevated border border-border px-3 py-2"
    />
    <input
      v-model.trim="draft.project"
      type="text"
      :placeholder="t('search.project_placeholder')"
      class="rounded bg-bg-elevated border border-border px-3 py-2"
    />
    <input
      v-model.trim="draft.scopes"
      type="text"
      :placeholder="t('admin.tokens.scopes_placeholder')"
      class="rounded bg-bg-elevated border border-border px-3 py-2"
    />
    <input
      v-model="draft.password"
      type="password"
      autocomplete="current-password"
      :placeholder="t('admin.tokens.password_placeholder')"
      :aria-label="t('password.confirm_action')"
      data-token-password
      class="rounded bg-bg-elevated border border-border px-3 py-2"
    />
    <button
      type="submit"
      :disabled="creating"
      class="px-3 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover disabled:opacity-60"
    >{{ t('common.create') }}</button>
  </form>

  <ErrorMessage v-if="error && loaded" :text="error" class="mb-3" />
  <p v-if="loading && !loaded" class="text-text-muted">{{ t('common.loading') }}</p>
  <ErrorMessage v-else-if="error && !loaded" :text="error" />

  <div v-else class="overflow-x-auto">
    <!-- Scrolls inside its box in a narrow window (IMP-156, M7). -->

    <table class="w-full text-sm">
      <thead class="text-text-muted text-xs uppercase tracking-wide">
        <tr>
          <th class="text-left py-2 px-3">{{ t('admin.col.name') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.owner') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.project') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.scopes') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.created') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.expires') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.last_used') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.self_improve') }}</th>
          <th class="text-right py-2 px-3">{{ t('admin.col.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="tok in tokens"
          :key="tok.id"
          class="border-t border-border"
        >
          <td class="py-2 px-3 font-medium">{{ tok.name }}</td>
          <td class="py-2 px-3">{{ ownerLabel(tok) }}</td>
          <td
            class="py-2 px-3"
            :title="inherits(tok) ? t('admin.tokens.inherit_hint') : undefined"
          >{{ scopeLabel(tok) }}</td>
          <td class="py-2 px-3">
            <span
              v-for="s in tok.scopes"
              :key="s"
              class="text-xs px-1.5 py-0.5 rounded border border-border mr-1"
            >{{ s }}</span>
          </td>
          <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="tok.created_at" /></td>
          <td class="py-2 px-3 text-xs whitespace-nowrap">
            <span :class="tok.expired ? 'text-warning' : ''"><DateTime v-if="tok.expires_at" :value="tok.expires_at" /><template v-else>—</template></span>
          </td>
          <td
            class="py-2 px-3 text-xs whitespace-nowrap"
            data-last-used
            :title="tok.last_used_at ? undefined : t('my_tokens.never_used_hint')"
          >
            <DateTime v-if="tok.last_used_at" :value="tok.last_used_at" /><template v-else>—</template>
          </td>
          <td class="py-2 px-3">
            <button
              type="button"
              class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover"
              :class="tok.self_improve_opt_in ? 'text-success' : 'text-text-muted'"
              :title="tok.self_improve_opt_in ? t('admin.tokens.optin_on') : t('admin.tokens.optin_off')"
              @click="toggleOptIn(tok)"
            >{{ tok.self_improve_opt_in ? t('admin.tokens.optin_yes') : t('admin.tokens.optin_no') }}</button>
          </td>
          <td class="py-2 px-3 text-right">
            <button
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="revoke(tok)"
            >{{ t('common.revoke') }}</button>
          </td>
        </tr>
      </tbody>
    </table>

  </div>
</template>
