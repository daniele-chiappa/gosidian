<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import {
  listUsers,
  disableUser,
  updateUserRole,
  updateUserTOTPPolicy,
  resetUserTOTP,
  createUser,
  type AdminUser,
  type CreateUserRequest,
} from '@/api/admin'
import { getUserAccess, VISIBILITY_LABEL, type AccessProject } from '@/api/access'

const users = ref<AdminUser[]>([])

// --- "View as" access preview: one expanded row at a time, fetched on demand ---
const expanded = ref<string | null>(null)
const accessRows = ref<AccessProject[]>([])
const accessLoading = ref(false)
const accessError = ref<string | null>(null)

async function toggleAccess(u: AdminUser) {
  if (expanded.value === u.id) {
    expanded.value = null
    return
  }
  expanded.value = u.id
  accessRows.value = []
  accessError.value = null
  accessLoading.value = true
  try {
    accessRows.value = (await getUserAccess(u.id)).projects
  } catch (e) {
    accessError.value = e instanceof Error ? e.message : 'Failed to load access'
  } finally {
    accessLoading.value = false
  }
}

function accessSummary(u: AdminUser): string {
  if (u.role === 'owner') return 'all projects'
  const r = u.projects_readable ?? 0
  const w = u.projects_writable ?? 0
  if (r === 0) return 'no project'
  return `${r} readable · ${w} writable`
}

function levelClass(level: string): string {
  return level === 'admin'
    ? 'bg-accent/20 text-accent'
    : level === 'write'
      ? 'bg-success/20 text-success'
      : 'border border-border text-text-muted'
}

function viaLabel(via: string[]): string {
  return via
    .map((v) => (v.startsWith('grant:') ? `grant (${v.slice(6)})` : v))
    .join(' + ')
}
const loading = ref(false)
const error = ref<string | null>(null)

// --- Create user form ---
const showCreate = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const created = ref<string | null>(null)
const showPassword = ref(false)
const copied = ref(false)
const newUser = reactive<CreateUserRequest>({
  username: '',
  password: '',
  role: 'member',
  totp_policy: '',
})

function resetCreate() {
  newUser.username = ''
  newUser.password = ''
  newUser.role = 'member'
  newUser.totp_policy = ''
  createError.value = null
  showPassword.value = false
  copied.value = false
}

// Strong password generated client-side via the Web Crypto API. The alphabet
// drops visually ambiguous characters (0/O, 1/l/I) since the admin has to read
// it out or paste it — there is no self-service change yet (IMP-063).
function generatePassword() {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%*-_'
  const len = 20
  const buf = new Uint32Array(len)
  crypto.getRandomValues(buf)
  let out = ''
  for (const n of buf) out += alphabet.charAt(n % alphabet.length)
  newUser.password = out
  showPassword.value = true
  copied.value = false
}

async function copyPassword() {
  if (!newUser.password) return
  try {
    await navigator.clipboard.writeText(newUser.password)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    /* clipboard blocked (e.g. insecure context) — the field is visible anyway */
  }
}

async function submitCreate() {
  createError.value = null
  if (!newUser.username.trim() || newUser.password.length < 8) {
    createError.value = 'Username is required and the password must be at least 8 characters.'
    return
  }
  creating.value = true
  try {
    const u = await createUser({
      username: newUser.username.trim(),
      password: newUser.password,
      role: newUser.role,
      totp_policy: newUser.totp_policy || undefined,
    })
    created.value = u.username
    resetCreate()
    showCreate.value = false
    await load()
  } catch (e) {
    createError.value = e instanceof Error ? e.message : 'Create failed'
  } finally {
    creating.value = false
  }
}

async function load() {
  loading.value = true
  error.value = null
  try {
    users.value = await listUsers()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load'
  } finally {
    loading.value = false
  }
}

async function disable(u: AdminUser) {
  if (!confirm(`Disable user "${u.username}"? They will be logged out and MCP tokens revoked.`)) return
  try {
    await disableUser(u.id)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Disable failed'
  }
}

async function changeRole(u: AdminUser, role: string) {
  if ((role !== 'member' && role !== 'guest') || role === u.role) return
  try {
    await updateUserRole(u.id, role)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Role change failed'
  }
}

async function changeTotpPolicy(u: AdminUser, policy: string) {
  try {
    await updateUserTOTPPolicy(u.id, policy)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'TOTP policy change failed'
  }
}

// Lost-authenticator escape hatch: clears the secret and the recovery codes,
// leaves policy and sessions alone (a required policy re-enrols the user at
// the next login).
async function resetTotp(u: AdminUser) {
  if (
    !confirm(
      `Reset two-factor for "${u.username}"? Their authenticator and recovery codes stop working; under a required policy they will enrol again at the next login.`,
    )
  )
    return
  try {
    await resetUserTOTP(u.id)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'TOTP reset failed'
  }
}

onMounted(load)
</script>

<template>
  <div class="space-y-4">
    <!-- Create user -->
    <section class="rounded-lg border border-border p-4">
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold">New user</h3>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded bg-accent text-accent-fg hover:bg-accent-hover"
          @click="showCreate ? (showCreate = false) : ((created = null), (showCreate = true))"
        >
          {{ showCreate ? 'Cancel' : '+ New user' }}
        </button>
      </div>

      <p v-if="created && !showCreate" class="mt-2 text-xs text-success">
        User “{{ created }}” created. Share the password securely — it can't be recovered later.
      </p>

      <form v-if="showCreate" class="mt-3 grid gap-3 sm:grid-cols-2" @submit.prevent="submitCreate">
        <label class="block text-sm">
          <span class="text-text-muted text-xs">Username</span>
          <input
            v-model.trim="newUser.username"
            type="text"
            autocomplete="off"
            required
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
          />
        </label>

        <label class="block text-sm">
          <span class="text-text-muted text-xs">Password (min 8)</span>
          <div class="mt-1 flex gap-1">
            <input
              v-model="newUser.password"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="new-password"
              required
              class="w-full rounded bg-bg-elevated border border-border px-3 py-2 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-accent"
            />
            <button
              type="button"
              class="px-2 rounded border border-border text-xs hover:bg-surface-hover"
              :title="showPassword ? 'Hide' : 'Show'"
              @click="showPassword = !showPassword"
            >{{ showPassword ? '🙈' : '👁' }}</button>
            <button
              type="button"
              class="px-2 rounded border border-border text-xs hover:bg-surface-hover"
              title="Generate a strong password"
              @click="generatePassword"
            >🎲</button>
            <button
              type="button"
              class="px-2 rounded border border-border text-xs hover:bg-surface-hover"
              :title="copied ? 'Copied' : 'Copy'"
              @click="copyPassword"
            >{{ copied ? '✓' : '⧉' }}</button>
          </div>
        </label>

        <label class="block text-sm">
          <span class="text-text-muted text-xs">Role</span>
          <select
            v-model="newUser.role"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
          >
            <option value="member">member</option>
            <option value="guest">guest</option>
          </select>
        </label>

        <label class="block text-sm">
          <span class="text-text-muted text-xs">TOTP policy</span>
          <select
            v-model="newUser.totp_policy"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
          >
            <option value="">inherit</option>
            <option value="enabled">required</option>
            <option value="disabled">exempt</option>
          </select>
        </label>

        <div class="sm:col-span-2 flex items-center gap-3">
          <button
            type="submit"
            :disabled="creating"
            class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
          >
            {{ creating ? 'Creating…' : 'Create user' }}
          </button>
          <p v-if="createError" class="text-sm text-danger">{{ createError }}</p>
        </div>
      </form>
    </section>

    <p v-if="loading" class="text-text-muted">Loading…</p>
    <p v-else-if="error" class="text-danger">{{ error }}</p>

    <table v-else class="w-full text-sm">
      <thead class="text-text-muted text-xs uppercase tracking-wide">
        <tr>
          <th class="text-left py-2 px-3">Username</th>
          <th class="text-left py-2 px-3">Role</th>
          <th class="text-left py-2 px-3">TOTP</th>
          <th class="text-left py-2 px-3">Access</th>
          <th class="text-left py-2 px-3">Created</th>
          <th class="text-left py-2 px-3">Status</th>
          <th class="text-right py-2 px-3">Actions</th>
        </tr>
      </thead>
      <tbody v-for="u in users" :key="u.id">
        <tr class="border-t border-border">
          <td class="py-2 px-3 font-medium">{{ u.username }}</td>
          <td class="py-2 px-3">
            <span
              v-if="u.role === 'owner' || u.disabled_at"
              class="text-xs px-2 py-0.5 rounded"
              :class="u.role === 'owner' ? 'bg-accent/20 text-accent' : 'border border-border'"
            >{{ u.role }}</span>
            <select
              v-else
              class="text-xs rounded bg-bg-elevated border border-border px-2 py-1 focus:outline-none focus:ring-1 focus:ring-accent"
              :value="u.role"
              @change="changeRole(u, ($event.target as HTMLSelectElement).value)"
            >
              <option value="member">member</option>
              <option value="guest">guest</option>
            </select>
          </td>
          <td class="py-2 px-3">
            <div class="flex items-center gap-2">
              <select
                v-if="u.role !== 'owner' && !u.disabled_at"
                class="text-xs rounded bg-bg-elevated border border-border px-2 py-1 focus:outline-none focus:ring-1 focus:ring-accent"
                :value="u.totp_policy || ''"
                @change="changeTotpPolicy(u, ($event.target as HTMLSelectElement).value)"
              >
                <option value="">inherit</option>
                <option value="enabled">required</option>
                <option value="disabled">exempt</option>
              </select>
              <span v-else class="text-xs text-text-muted">{{ u.totp_policy || 'inherit' }}</span>
              <span v-if="u.totp_enrolled" class="text-xs text-success" title="TOTP enrolled">●</span>
              <button
                v-if="u.totp_enrolled && !u.disabled_at"
                type="button"
                class="text-xs px-2 py-0.5 rounded text-warning hover:bg-surface-hover"
                title="Clear the secret and recovery codes (lost authenticator)"
                @click="resetTotp(u)"
              >Reset</button>
            </div>
          </td>
          <td class="py-2 px-3">
            <div class="flex items-center gap-2">
              <span class="text-xs" :class="u.role === 'owner' ? 'text-text-muted' : ''">{{ accessSummary(u) }}</span>
              <button
                v-if="u.role !== 'owner' && !u.disabled_at"
                type="button"
                class="text-xs px-2 py-0.5 rounded border border-border hover:bg-surface-hover"
                :title="expanded === u.id ? 'Hide the projects this account can see' : 'Show the projects this account can see, and why'"
                @click="toggleAccess(u)"
              >{{ expanded === u.id ? 'Hide' : 'View' }}</button>
            </div>
          </td>
          <td class="py-2 px-3 font-mono text-xs">{{ u.created_at }}</td>
          <td class="py-2 px-3">
            <span
              v-if="u.disabled_at"
              class="text-xs text-warning"
            >disabled {{ u.disabled_at }}</span>
            <span v-else class="text-xs text-success">active</span>
          </td>
          <td class="py-2 px-3 text-right">
            <button
              v-if="!u.disabled_at && u.role !== 'owner'"
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="disable(u)"
            >Disable</button>
          </td>
        </tr>
        <!-- "View as": what this account sees and why -->
        <tr v-if="expanded === u.id" class="bg-bg-elevated/40">
          <td colspan="7" class="px-3 py-2">
            <p v-if="accessLoading" class="text-xs text-text-muted">Loading…</p>
            <p v-else-if="accessError" class="text-xs text-danger">{{ accessError }}</p>
            <p v-else-if="accessRows.length === 0" class="text-xs text-text-muted">
              This account cannot see any project. Make a project internal or public, or add a grant from Projects → Members.
            </p>
            <ul v-else class="flex flex-wrap gap-2">
              <li
                v-for="row in accessRows"
                :key="row.name"
                class="inline-flex items-center gap-1.5 rounded border border-border px-2 py-1 text-xs"
                :title="`${VISIBILITY_LABEL[row.visibility]} project — ${row.level} via ${viaLabel(row.via)}`"
              >
                <span class="font-medium">{{ row.name }}</span>
                <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded" :class="levelClass(row.level)">{{ row.level }}</span>
                <span class="text-text-muted">{{ viaLabel(row.via) }}</span>
              </li>
            </ul>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
