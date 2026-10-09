<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Check, Copy, Dices, Eye, EyeOff, ShieldCheck } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { resetUserPassword } from '@/api/password'
import {
  listUsers,
  disableUser,
  updateUserRole,
  updateUserTOTPPolicy,
  resetUserTOTP,
  createUser,
  updateUserFlags,
  createPersonalProject,
  type AdminUser,
  type CreateUserRequest,
} from '@/api/admin'
import { getUserAccess, visibilityLabel, roleLabel, type AccessProject } from '@/api/access'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'
import { formatDateTime } from '@/api/format'
import { confirmAction } from '@/composables/useConfirm'

const users = ref<AdminUser[]>([])
const { t, te } = useI18n()

// --- Reset password (IMP-063): a temporary password the account changes at
// its next request, confirmed with the owner's own (IMP-088). ---
const resetFor = ref<string | null>(null)
const resetPassword = ref('')
const resetOwnerPassword = ref('')
const resetBusy = ref(false)
const resetError = ref<string | null>(null)
const resetNotice = ref<string | null>(null)
function openReset(u: AdminUser) {
  resetFor.value = resetFor.value === u.id ? null : u.id
  resetPassword.value = ''
  resetOwnerPassword.value = ''
  resetError.value = null
}
function generateResetPassword() {
  resetPassword.value = strongPassword()
}
async function submitReset(u: AdminUser) {
  if (resetPassword.value.length < 8 || !resetOwnerPassword.value || resetBusy.value) return
  resetBusy.value = true
  resetError.value = null
  try {
    await resetUserPassword(u.id, resetPassword.value, resetOwnerPassword.value)
    resetNotice.value = t('password.reset_done', { user: u.username })
    resetFor.value = null
    await load()
  } catch (e) {
    resetError.value = errorText(e, t, t('password.reset_failed'))
  } finally {
    resetOwnerPassword.value = ''
    resetBusy.value = false
  }
}

// --- "View as" access preview: one expanded row at a time, fetched on demand ---
const expanded = ref<string | null>(null)
const accessRows = ref<AccessProject[]>([])
const accessLoading = ref(false)
const accessError = ref<string | null>(null)
// Only the last request fills the preview: the answer for an account opened
// before landed under the one opened after (BUG-116, S6-8).
let accessGen = 0

async function toggleAccess(u: AdminUser) {
  if (expanded.value === u.id) {
    expanded.value = null
    accessGen++
    return
  }
  expanded.value = u.id
  accessRows.value = []
  await loadAccess(u.id)
}

async function loadAccess(id: string) {
  const gen = ++accessGen
  accessError.value = null
  accessLoading.value = true
  try {
    const rows = (await getUserAccess(id)).projects
    if (gen === accessGen) accessRows.value = rows
  } catch (e) {
    if (gen === accessGen) accessError.value = errorText(e, t, t('members.load_failed'))
  } finally {
    if (gen === accessGen) accessLoading.value = false
  }
}

function accessSummary(u: AdminUser): string {
  if (u.role === 'owner') return t('admin.users.all_projects')
  const r = u.projects_readable ?? 0
  const w = u.projects_writable ?? 0
  if (r === 0) return t('admin.users.no_project')
  return t('admin.users.readable_writable', { r, w })
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
    .map((v) => {
      if (v.startsWith('grant:')) return t('admin.users.via_grant', { level: v.slice(6) })
      if (v.startsWith('team:')) {
        const i = v.lastIndexOf(':')
        return t('admin.users.via_team', { team: v.slice(5, i), level: v.slice(i + 1) })
      }
      // A reason the catalogue does not know yet shows as the server names it.
      return te(`admin.users.via.${v}`) ? t(`admin.users.via.${v}`) : v
    })
    .join(' + ')
}
const loading = ref(false)
// Only the first load puts "Loading…" in place of the list: a reload after
// an action took the whole list down and back, focus and scroll with it.
const loaded = ref(false)
const error = ref<string | null>(null)

// --- Create user form ---
const showCreate = ref(false)
const creating = ref(false)
const createError = ref<string | null>(null)
const created = ref<string | null>(null)
const createdArchived = ref<string | null>(null)
const createdArchivedProject = ref<string | null>(null)
const createdProjectWarning = ref<string | null>(null)
const showPassword = ref(false)
const copied = ref(false)
const newUser = reactive<CreateUserRequest>({
  username: '',
  password: '',
  role: 'member',
  totp_policy: '',
  restricted: true,
  can_create_projects: true,
})

function resetCreate() {
  newUser.username = ''
  newUser.password = ''
  newUser.role = 'member'
  newUser.totp_policy = ''
  newUser.restricted = true
  newUser.can_create_projects = true
  createError.value = null
  showPassword.value = false
  copied.value = false
}

// Strong password generated client-side via the Web Crypto API. The alphabet
// drops visually ambiguous characters (0/O, 1/l/I) since the admin has to read
// it out or paste it; the account replaces it at its first sign-in (IMP-063).
function strongPassword(): string {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%*-_'
  const len = 20
  const buf = new Uint32Array(len)
  crypto.getRandomValues(buf)
  let out = ''
  for (const n of buf) out += alphabet.charAt(n % alphabet.length)
  return out
}
function generatePassword() {
  newUser.password = strongPassword()
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
    createError.value = t('admin.users.create_invalid')
    return
  }
  creating.value = true
  try {
    const u = await createUser({
      username: newUser.username.trim(),
      password: newUser.password,
      role: newUser.role,
      totp_policy: newUser.totp_policy || undefined,
      restricted: newUser.restricted,
      can_create_projects: newUser.can_create_projects,
    })
    created.value = u.username
    createdArchived.value = u.archived_username ?? null
    createdArchivedProject.value = u.archived_personal_project ?? null
    createdProjectWarning.value = u.personal_project_warning ?? null
    resetCreate()
    showCreate.value = false
    await load()
  } catch (e) {
    createError.value = errorText(e, t, t('my_tokens.create_failed'))
  } finally {
    creating.value = false
  }
}

async function load() {
  loading.value = true
  error.value = null
  try {
    users.value = await listUsers()
    loaded.value = true
  } catch (e) {
    error.value = errorText(e, t, t('admin.users.load_failed'))
  } finally {
    loading.value = false
  }
  // A role or a flag changed: the open preview follows (BUG-116, S6-8).
  if (expanded.value) await loadAccess(expanded.value)
}

async function disable(u: AdminUser) {
  if (!(await confirmAction(t('admin.users.confirm_disable', { user: u.username }), { confirmLabel: t('common.disable_button') })))
    return
  try {
    await disableUser(u.id)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('admin.users.disable_failed'))
  }
}

async function changeRole(u: AdminUser, role: string, select?: HTMLSelectElement) {
  if ((role !== 'member' && role !== 'guest') || role === u.role) return
  // Read-only revokes every MCP token of the account: asked first, as an
  // arrow key on the select was enough (BUG-116, S6-9).
  if (role === 'guest' && !(await confirmAction(t('users.confirm_guest', { user: u.username })))) {
    if (select) select.value = u.role
    return
  }
  try {
    await updateUserRole(u.id, role)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('admin.users.role_failed'))
  }
}

async function toggleRestricted(u: AdminUser) {
  try {
    await updateUserFlags(u.id, { restricted: !u.restricted })
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('admin.tokens.update_failed'))
  }
}

async function toggleCanCreate(u: AdminUser) {
  try {
    await updateUserFlags(u.id, { can_create_projects: !u.can_create_projects })
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('admin.tokens.update_failed'))
  }
}

async function provisionPersonal(u: AdminUser) {
  try {
    await createPersonalProject(u.id)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('admin.users.personal_failed'))
  }
}

async function changeTotpPolicy(u: AdminUser, policy: string) {
  try {
    await updateUserTOTPPolicy(u.id, policy)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('admin.users.totp_policy_failed'))
  }
}

// Lost-authenticator escape hatch: clears the secret and the recovery codes,
// leaves policy and sessions alone (a required policy re-enrols the user at
// the next login).
async function resetTotp(u: AdminUser) {
  if (!(await confirmAction(t('admin.users.confirm_totp_reset', { user: u.username })))) return
  try {
    await resetUserTOTP(u.id)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('admin.users.totp_reset_failed'))
  }
}

onMounted(load)
</script>

<template>
  <div class="space-y-4">
    <!-- Create user -->
    <section class="rounded-lg border border-border p-4">
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold">{{ t('admin.users.new') }}</h3>
        <button
          type="button"
          class="h-control-sm text-xs px-2 rounded bg-accent text-accent-fg hover:bg-accent-hover"
          @click="showCreate ? (showCreate = false) : ((created = null), (showCreate = true))"
        >
          {{ showCreate ? t('common.cancel') : t('admin.users.new_button') }}
        </button>
      </div>

      <p v-if="created && !showCreate" class="mt-2 text-xs text-success">
        {{ t('admin.users.created', { user: created }) }}
        {{ t('password.created_hint') }}
        <template v-if="createdArchived">
          {{ t('admin.users.archived', { name: createdArchived }) }}
        </template>
        <template v-if="createdArchivedProject">
          {{ t('admin.users.archived_project', { name: createdArchivedProject }) }}
        </template>
      </p>
      <p v-if="created && !showCreate && createdProjectWarning" class="mt-1 text-xs text-warning">
        {{ createdProjectWarning }}
      </p>

      <form v-if="showCreate" class="mt-3 grid grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-3" @submit.prevent="submitCreate">
        <label class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('common.username') }}</span>
          <input
            v-model.trim="newUser.username"
            type="text"
            autocomplete="off"
            required
            class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>

        <label class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('signup.password') }}</span>
          <div class="mt-1 flex gap-1">
            <input
              v-model="newUser.password"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="new-password"
              required
              class="h-control py-0 w-full rounded bg-bg-elevated border border-border px-3 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-focus"
            />
            <button
              type="button"
              class="px-2 rounded border border-border text-xs hover:bg-surface-hover"
              :title="showPassword ? t('admin.users.hide') : t('admin.users.show')"
              :aria-label="showPassword ? t('admin.users.hide') : t('admin.users.show')"
              @click="showPassword = !showPassword"
            ><component :is="showPassword ? EyeOff : Eye" class="h-4 w-4" aria-hidden="true" /></button>
            <button
              type="button"
              class="px-2 rounded border border-border text-xs hover:bg-surface-hover"
              :title="t('admin.users.generate')"
              :aria-label="t('admin.users.generate')"
              @click="generatePassword"
            ><Dices class="h-4 w-4" aria-hidden="true" /></button>
            <button
              type="button"
              class="px-2 rounded border border-border text-xs hover:bg-surface-hover"
              :title="copied ? t('common.copied') : t('common.copy')"
              :aria-label="copied ? t('common.copied') : t('common.copy')"
              @click="copyPassword"
            ><component :is="copied ? Check : Copy" class="h-4 w-4" aria-hidden="true" /></button>
          </div>
        </label>

        <label class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('admin.users.role') }}</span>
          <select
            v-model="newUser.role"
            class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
          >
            <option value="member">{{ roleLabel('member') }} — {{ t('admin.users.member_hint') }}</option>
            <option value="guest">{{ roleLabel('guest') }} — {{ t('admin.users.guest_hint') }}</option>
          </select>
        </label>

        <label class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('admin.users.totp_policy') }}</span>
          <select
            v-model="newUser.totp_policy"
            class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
          >
            <option value="">{{ t('admin.users.totp_inherit') }}</option>
            <option value="enabled">{{ t('admin.users.totp_required') }}</option>
            <option value="disabled">{{ t('admin.users.totp_exempt') }}</option>
          </select>
        </label>

        <label class="flex items-start gap-2 text-sm">
          <input v-model="newUser.restricted" type="checkbox" class="mt-1" />
          <span>
            <span class="block">{{ t('admin.users.restricted_label') }}</span>
            <span class="text-xs text-text-muted">{{ t('admin.users.restricted_hint') }}</span>
          </span>
        </label>
        <label class="flex items-start gap-2 text-sm">
          <input v-model="newUser.can_create_projects" type="checkbox" class="mt-1" :disabled="newUser.role === 'guest'" />
          <span>
            <span class="block">{{ t('admin.users.can_create') }}</span>
            <span class="text-xs text-text-muted">{{ t('admin.users.can_create_hint') }}</span>
          </span>
        </label>

        <div class="col-span-full flex flex-wrap items-center gap-3">
          <button
            type="submit"
            :disabled="creating"
            class="h-control rounded bg-accent text-accent-fg px-3 text-sm hover:bg-accent-hover disabled:opacity-60"
          >
            {{ creating ? t('signup.creating') : t('admin.users.create') }}
          </button>
          <ErrorMessage v-if="createError" :text="createError" class="text-sm" />
        </div>
      </form>
    </section>

    <p v-if="resetNotice" class="text-xs text-success" data-reset-notice>{{ resetNotice }}</p>
    <ErrorMessage v-if="error && loaded" :text="error" class="mb-3" />
    <p v-if="loading && !loaded" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-else-if="error && !loaded" :text="error" />

    <div v-else class="overflow-x-auto">
      <!-- Scrolls inside its box in a narrow window (IMP-156, M7). -->

      <table class="w-full text-sm">
        <thead class="text-text-muted text-xs [&_th]:font-medium">
          <tr>
            <th class="text-left py-2 px-3">{{ t('common.username') }}</th>
            <th class="text-left py-2 px-3">{{ t('admin.users.role') }}</th>
            <th class="text-left py-2 px-3">TOTP</th>
            <th class="text-left py-2 px-3">{{ t('admin.users.access') }}</th>
            <th class="text-left py-2 px-3">{{ t('admin.users.flags') }}</th>
            <th class="text-left py-2 px-3">{{ t('admin.col.created') }}</th>
            <th class="text-left py-2 px-3">{{ t('admin.col.status') }}</th>
            <th class="text-right py-2 px-3">{{ t('admin.col.actions') }}</th>
          </tr>
        </thead>
        <tbody v-for="u in users" :key="u.id">
          <tr class="border-t border-border">
            <td class="py-2 px-3 font-medium whitespace-nowrap">{{ u.username }}</td>
            <td class="py-2 px-3">
              <span
                v-if="u.role === 'owner' || u.disabled_at"
                class="h-control-sm text-xs px-2 rounded"
                :class="u.role === 'owner' ? 'bg-accent/20 text-accent' : 'border border-border'"
              >{{ roleLabel(u.role) }}</span>
              <select
                v-else
                class="h-control-sm py-0 text-xs rounded bg-bg-elevated border border-border px-2 focus:outline-none focus:ring-1 focus:ring-focus"
                :value="u.role"
                @change="changeRole(u, ($event.target as HTMLSelectElement).value, $event.target as HTMLSelectElement)"
              >
                <option value="member">{{ roleLabel('member') }}</option>
                <option value="guest">{{ roleLabel('guest') }}</option>
              </select>
            </td>
            <td class="py-2 px-3">
              <div class="flex items-center gap-2">
                <select
                  v-if="u.role !== 'owner' && !u.disabled_at"
                  class="h-control-sm py-0 text-xs rounded bg-bg-elevated border border-border px-2 focus:outline-none focus:ring-1 focus:ring-focus"
                  :value="u.totp_policy || ''"
                  @change="changeTotpPolicy(u, ($event.target as HTMLSelectElement).value)"
                >
                  <option value="">{{ t('admin.users.totp_inherit') }}</option>
                  <option value="enabled">{{ t('admin.users.totp_required') }}</option>
                  <option value="disabled">{{ t('admin.users.totp_exempt') }}</option>
                </select>
                <span v-else class="text-xs text-text-muted whitespace-nowrap">{{ t(`admin.users.totp_policy_name.${u.totp_policy || 'inherit'}`) }}</span>
                <span
                  v-if="u.totp_enrolled"
                  class="shrink-0 text-success"
                  role="img"
                  :title="t('admin.users.totp_enrolled')"
                  :aria-label="t('admin.users.totp_enrolled')"
                ><ShieldCheck class="h-4 w-4" aria-hidden="true" /></span>
                <!-- The owner's own two-factor comes off from Settings, with
                     the password: the server refuses it here. -->
                <button
                  v-if="u.totp_enrolled && !u.disabled_at && u.role !== 'owner'"
                  type="button"
                  class="h-control-sm text-xs px-2 rounded text-warning hover:bg-surface-hover"
                  :title="t('admin.users.totp_reset_hint')"
                  @click="resetTotp(u)"
                >{{ t('admin.users.totp_reset') }}</button>
              </div>
            </td>
            <td class="py-2 px-3">
              <div class="flex items-center gap-2">
                <span class="text-xs whitespace-nowrap" :class="u.role === 'owner' ? 'text-text-muted' : ''">{{ accessSummary(u) }}</span>
                <button
                  v-if="u.role !== 'owner' && !u.disabled_at"
                  type="button"
                  class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover"
                  :title="expanded === u.id ? t('admin.users.hide_access_hint') : t('admin.users.view_access_hint')"
                  @click="toggleAccess(u)"
                >{{ expanded === u.id ? t('admin.users.hide_access') : t('admin.users.view_access') }}</button>
              </div>
            </td>
            <td class="py-2 px-3">
              <div v-if="u.role !== 'owner'" class="flex flex-wrap items-center gap-1">
                <button
                  type="button"
                  class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border"
                  :class="u.restricted ? 'border-warning text-warning' : 'border-border text-text-muted'"
                  :disabled="!!u.disabled_at"
                  :title="u.restricted ? t('admin.users.restricted_on') : t('admin.users.restricted_off')"
                  @click="toggleRestricted(u)"
                >{{ u.restricted ? t('admin.users.restricted') : t('admin.users.open') }}</button>
                <button
                  v-if="u.role === 'member'"
                  type="button"
                  class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border"
                  :class="u.can_create_projects ? 'border-border text-text-muted' : 'border-warning text-warning'"
                  :disabled="!!u.disabled_at"
                  :title="u.can_create_projects ? t('admin.users.creates_on') : t('admin.users.creates_off')"
                  @click="toggleCanCreate(u)"
                >{{ u.can_create_projects ? t('admin.users.creates') : t('admin.users.no_create') }}</button>
                <span
                  v-if="u.personal_project"
                  class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border border-border text-text-muted"
                  :title="t('admin.users.personal_title', { name: u.personal_project })"
                >~{{ u.personal_project }}</span>
                <button
                  v-else-if="u.role === 'member' && !u.disabled_at"
                  type="button"
                  class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border border-border text-text-muted hover:bg-surface-hover"
                  :title="t('admin.users.personal_create_hint')"
                  @click="provisionPersonal(u)"
                >{{ t('admin.users.personal_create') }}</button>
              </div>
              <span v-else class="text-xs text-text-muted">—</span>
            </td>
            <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="u.created_at" /></td>
            <td class="py-2 px-3">
              <span
                v-if="u.disabled_at"
                class="text-xs text-warning"
              >{{ t('admin.users.disabled', { when: formatDateTime(u.disabled_at) }) }}</span>
              <span v-else class="text-xs text-success">{{ t('admin.users.active') }}</span>
              <span
                v-if="u.must_change_password && !u.disabled_at"
                class="ml-1 text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border border-warning text-warning"
              >{{ t('password.must_change') }}</span>
            </td>
            <td class="py-2 px-3 text-right whitespace-nowrap">
              <button
                v-if="!u.disabled_at && u.role !== 'owner' && u.auth_source !== 'ldap'"
                type="button"
                class="h-control-sm text-xs px-2 rounded hover:bg-surface-hover"
                :data-reset-password="u.username"
                @click="openReset(u)"
              >{{ t('password.reset_title') }}</button>
              <button
                v-if="!u.disabled_at && u.role !== 'owner'"
                type="button"
                class="h-control-sm text-xs px-2 rounded text-danger hover:bg-surface-hover"
                @click="disable(u)"
              >{{ t('admin.users.disable') }}</button>
            </td>
          </tr>
          <!-- Reset password: temporary, confirmed with the owner's own -->
          <tr v-if="resetFor === u.id" class="bg-bg-elevated/40">
            <td colspan="8" class="px-3 py-2">
              <form class="flex flex-wrap items-end gap-2" data-reset-form @submit.prevent="submitReset(u)">
                <label class="block text-xs">
                  <span class="text-text-muted">{{ t('password.reset_new') }}</span>
                  <span class="mt-1 flex gap-1">
                    <input
                      v-model="resetPassword"
                      type="text"
                      autocomplete="off"
                      name="temporary-password"
                      class="h-control py-0 w-56 rounded bg-bg-elevated border border-border px-2 font-mono"
                    />
                    <button
                      type="button"
                      class="h-control rounded border border-border px-2 hover:bg-surface-hover"
                      :title="t('admin.users.generate')"
                      :aria-label="t('admin.users.generate')"
                      @click="generateResetPassword"
                    ><Dices class="h-4 w-4" aria-hidden="true" /></button>
                  </span>
                </label>
                <label class="block text-xs">
                  <span class="text-text-muted">{{ t('password.reset_owner') }}</span>
                  <input
                    v-model="resetOwnerPassword"
                    type="password"
                    autocomplete="current-password"
                    name="owner-password"
                    class="h-control py-0 mt-1 w-56 rounded bg-bg-elevated border border-border px-2"
                  />
                </label>
                <button
                  type="submit"
                  :disabled="resetBusy || resetPassword.length < 8 || !resetOwnerPassword"
                  class="h-control rounded bg-accent text-accent-fg px-3 text-xs hover:bg-accent-hover disabled:opacity-60"
                >{{ t('password.reset_submit') }}</button>
                <ErrorMessage v-if="resetError" :text="resetError" class="text-xs" />
              </form>
            </td>
          </tr>
          <!-- "View as": what this account sees and why -->
          <tr v-if="expanded === u.id" class="bg-bg-elevated/40">
            <td colspan="8" class="px-3 py-2">
              <p v-if="accessLoading" class="text-xs text-text-muted">{{ t('common.loading') }}</p>
              <ErrorMessage v-else-if="accessError" :text="accessError" class="text-xs" />
              <p v-else-if="accessRows.length === 0" class="text-xs text-text-muted">
                {{ t('admin.users.sees_nothing') }}
              </p>
              <ul v-else class="flex flex-wrap gap-2">
                <li
                  v-for="row in accessRows"
                  :key="row.name"
                  class="inline-flex items-center gap-1.5 rounded border border-border px-2 py-1 text-xs"
                  :title="t('admin.users.access_row', { visibility: visibilityLabel(row.visibility), level: t(`members.level_name.${row.level}`), via: viaLabel(row.via) })"
                >
                  <span class="font-medium">{{ row.name }}</span>
                  <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded" :class="levelClass(row.level)">{{ t(`members.level_name.${row.level}`) }}</span>
                  <span class="text-text-muted">{{ viaLabel(row.via) }}</span>
                </li>
              </ul>
            </td>
          </tr>
        </tbody>
      </table>

    </div>
  </div>
</template>
