<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSettings, updateSettings, type Settings } from '@/api/settings'
import { apiErrorMessage } from '@/api/client'
import PasswordChange from '@/components/domain/PasswordChange.vue'
import { useAuthStore } from '@/stores/auth'
import { useUIStore, type LocaleCode, type ThemePreset } from '@/stores/ui'
import TotpEnroll from '@/components/domain/TotpEnroll.vue'
import MyTokens from '@/components/domain/MyTokens.vue'
import RecoveryCodes from '@/components/domain/RecoveryCodes.vue'
import { disenrollTOTP, regenerateRecoveryCodes } from '@/api/totp'

const auth = useAuthStore()
const ui = useUIStore()
const { t } = useI18n()

// The account's own password (IMP-063); an LDAP account's is the directory's.
const isLdap = computed(() => auth.user?.auth_source === 'ldap')
const passwordNotice = ref<string | null>(null)
function onPasswordChanged() {
  passwordNotice.value = t('password.changed')
}

const totpError = ref<string | null>(null)
function onTotpEnrolled(codeCount: number) {
  auth.setEnrolled(true)
  auth.setRecoveryCodesRemaining(codeCount)
}
// Removing the second factor asks for the password (IMP-088): a stolen
// session alone must not take it away.
const disableOpen = ref(false)
const disablePassword = ref('')
async function disableTotp() {
  if (!disablePassword.value) return
  totpError.value = null
  try {
    await disenrollTOTP(disablePassword.value)
    auth.setEnrolled(false)
    disableOpen.value = false
  } catch (e) {
    totpError.value = apiErrorMessage(e, 'Failed to disable two-factor')
  } finally {
    disablePassword.value = ''
  }
}

// Recovery-code regeneration: gated on a current TOTP (the session alone must
// not be able to mint itself a lasting second factor); the fresh set is shown
// once through RecoveryCodes, then the counter is refreshed.
const regenOpen = ref(false)
const regenCode = ref('')
const regenBusy = ref(false)
const regenCodes = ref<string[]>([])
async function regenerate() {
  if (!regenCode.value.trim() || regenBusy.value) return
  regenBusy.value = true
  totpError.value = null
  try {
    const codes = await regenerateRecoveryCodes(regenCode.value.trim())
    regenCodes.value = codes
    auth.setRecoveryCodesRemaining(codes.length)
    regenOpen.value = false
    regenCode.value = ''
  } catch (e) {
    totpError.value = e instanceof Error ? e.message : 'Failed to regenerate recovery codes'
  } finally {
    regenBusy.value = false
  }
}
function cancelRegen() {
  regenOpen.value = false
  regenCode.value = ''
}

interface PresetOption { value: ThemePreset; label: string; tone: 'dark' | 'light' }
const presetOptions: PresetOption[] = [
  { value: 'catppuccin-mocha', label: 'Catppuccin Mocha', tone: 'dark' },
  { value: 'tokyo-night', label: 'Tokyo Night', tone: 'dark' },
  { value: 'catppuccin-latte', label: 'Catppuccin Latte', tone: 'light' },
  { value: 'solarized-light', label: 'Solarized Light', tone: 'light' },
  { value: 'custom', label: 'Custom (default = Mocha)', tone: 'dark' },
]
interface LocaleOption { value: LocaleCode; label: string }
const localeOptions: LocaleOption[] = [
  { value: 'it', label: 'Italiano' },
  { value: 'en', label: 'English' },
  { value: 'es', label: 'Español' },
  { value: 'fr', label: 'Français' },
  { value: 'de', label: 'Deutsch' },
]
// The selector offers only the languages the operator enables (IMP-148).
const enabledLocaleOptions = computed(() =>
  localeOptions.filter((l) => ui.enabledLocales.includes(l.value)),
)
const data = ref<Settings | null>(null)
const draft = reactive<{
  git: {
    enabled: boolean
    remote: string
    branch: string
    debounce_ms: number
    push: boolean
    token_env: string
  }
  trash: { enabled: boolean; retention_ms: number }
  i18n: { default_lang: string; enabled_langs: string[] }
  totp_mode: string
  default_visibility: string
  personal_projects: boolean
}>({
  git: { enabled: false, remote: '', branch: '', debounce_ms: 30000, push: false, token_env: '' },
  trash: { enabled: false, retention_ms: 0 },
  i18n: { default_lang: 'en', enabled_langs: [] },
  totp_mode: 'off',
  default_visibility: 'private',
  personal_projects: true,
})
// The default is picked among the languages ticked below; the current one
// stays listed even when it is not (an environment variable may set it).
const defaultLangOptions = computed(() =>
  localeOptions.filter(
    (l) => draft.i18n.enabled_langs.includes(l.value) || l.value === draft.i18n.default_lang,
  ),
)
const loading = ref(false)
const saving = ref(false)
const message = ref<string | null>(null)
const error = ref<string | null>(null)

// Settings a GOSIDIAN_* environment variable sets: shown with their effective
// value but read-only, and left out of the save (the env would win anyway).
const envOverrides = computed(() => data.value?.env_overrides ?? [])
function fromEnv(key: string): boolean {
  return envOverrides.value.includes(key)
}
function withoutEnvFields<T extends object>(body: T): T {
  for (const key of envOverrides.value) {
    const parts = key.split('.')
    const last = parts.pop()
    let node = body as Record<string, unknown> | undefined
    for (const p of parts) node = node?.[p] as Record<string, unknown> | undefined
    if (node && last) delete node[last]
  }
  return body
}

function hydrate(s: Settings) {
  data.value = s
  draft.git = {
    enabled: s.git.enabled,
    remote: s.git.remote,
    branch: s.git.branch,
    debounce_ms: s.git.debounce_ms,
    push: s.git.push,
    token_env: s.git.token_env,
  }
  draft.trash = { enabled: s.trash.enabled, retention_ms: s.trash.retention_ms }
  draft.i18n = {
    default_lang: s.i18n.default_lang,
    enabled_langs: [...(s.i18n.enabled_langs ?? [])],
  }
  draft.totp_mode = s.totp_mode ?? 'off'
  draft.default_visibility = s.default_visibility || 'private'
  draft.personal_projects = s.personal_projects ?? true
}

async function load() {
  loading.value = true
  error.value = null
  try {
    hydrate(await getSettings())
  } catch (e) {
    error.value = apiErrorMessage(e, 'Failed to load settings')
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  error.value = null
  message.value = null
  try {
    const result = await updateSettings(withoutEnvFields({
      git: { ...draft.git },
      trash: { ...draft.trash },
      i18n: {
        default_lang: draft.i18n.default_lang,
        enabled_langs: [...draft.i18n.enabled_langs],
      },
      totp_mode: draft.totp_mode,
      default_visibility: draft.default_visibility,
      personal_projects: draft.personal_projects,
    }))
    hydrate(result)
    message.value = 'Saved.'
    // The languages apply at once: the selector above follows them.
    await ui.refreshLocales()
  } catch (e) {
    error.value = apiErrorMessage(e, 'Save failed')
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="p-8 max-w-3xl mx-auto">
    <h1 class="text-2xl font-semibold mb-1">Settings</h1>
    <p
      v-if="!auth.isOwner"
      class="text-sm text-text-muted mb-6"
    >
      Server settings are read-only for your role; your own two-factor setup and MCP tokens below are yours to change.
    </p>

    <fieldset
      v-if="!auth.isAnonymous"
      class="rounded border border-border bg-surface p-4 space-y-3 mb-6"
      data-password-section
    >
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">
        {{ t('password.section') }}
      </legend>
      <p v-if="isLdap" class="text-sm text-text-muted">
        {{ t('password.ldap') }}
      </p>
      <template v-else>
        <PasswordChange @done="onPasswordChanged" />
        <p v-if="passwordNotice" class="text-sm text-success">
          {{ passwordNotice }}
        </p>
      </template>
    </fieldset>

    <fieldset class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">Two-factor (TOTP)</legend>
      <label v-if="auth.isOwner" class="block text-sm">
        <span class="text-text-muted">Global policy</span>
        <select
          v-model="draft.totp_mode"
          :disabled="fromEnv('totp_mode')"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
          @change="save"
        >
          <option value="off">Off — two-factor disabled</option>
          <option value="optional">Optional — users may enable it</option>
          <option value="required">Required — all users must enable it</option>
        </select>
        <span class="text-xs text-text-muted">Per-user overrides are set in Admin → Users.</span>
      </label>
      <hr v-if="auth.isOwner" class="border-border" />
      <template v-if="auth.user?.totp_enrolled">
        <p class="text-sm text-success">Two-factor authentication is enabled for your account.</p>
        <p class="text-sm text-text-muted">
          Recovery codes remaining:
          <span class="font-medium text-text">{{ auth.user?.recovery_codes_remaining ?? 0 }}</span>
          <span v-if="(auth.user?.recovery_codes_remaining ?? 0) <= 2" class="text-warning">
            — running low, regenerate them soon
          </span>
        </p>
        <RecoveryCodes v-if="regenCodes.length" :codes="regenCodes" @done="regenCodes = []" />
        <template v-else>
          <button
            v-if="!regenOpen"
            type="button"
            class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
            @click="regenOpen = true"
          >Regenerate recovery codes…</button>
          <div v-else class="space-y-2">
            <p class="text-xs text-text-muted">
              Enter a current code from your authenticator. Every existing recovery code stops working.
            </p>
            <div class="flex gap-2">
              <input
                v-model.trim="regenCode"
                inputmode="numeric"
                autocomplete="one-time-code"
                placeholder="123 456"
                class="w-40 rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
                @keyup.enter="regenerate"
              />
              <button
                type="button"
                :disabled="regenBusy || !regenCode"
                class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
                @click="regenerate"
              >Regenerate</button>
              <button
                type="button"
                class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
                @click="cancelRegen"
              >Cancel</button>
            </div>
          </div>
        </template>
        <button
          v-if="!disableOpen"
          type="button"
          class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
          @click="disableOpen = true"
        >Disable two-factor…</button>
        <div v-else class="flex flex-wrap items-end gap-2" data-totp-disable>
          <label class="block text-sm">
            <span class="text-text-muted">{{ t('password.confirm_action') }}</span>
            <input
              v-model="disablePassword"
              type="password"
              autocomplete="current-password"
              class="mt-1 w-56 rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
              @keyup.enter="disableTotp"
            />
          </label>
          <button
            type="button"
            :disabled="!disablePassword"
            class="rounded bg-danger text-white px-3 py-2 text-sm disabled:opacity-60"
            @click="disableTotp"
          >Disable two-factor</button>
          <button
            type="button"
            class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
            @click="disableOpen = false; disablePassword = ''"
          >Cancel</button>
        </div>
        <p v-if="totpError" class="text-sm text-danger">{{ totpError }}</p>
      </template>
      <TotpEnroll v-else @done="onTotpEnrolled" />
    </fieldset>

    <fieldset v-if="auth.isOwner" class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">Project access</legend>
      <label class="block text-sm">
        <span class="text-text-muted">Default visibility for new projects</span>
        <select
          v-model="draft.default_visibility"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
          @change="save"
        >
          <option value="private">Private — only accounts with a grant (and the owner)</option>
          <option value="internal">Internal — every member can read; writing takes a grant</option>
          <option value="public">Public — every account can read, guests included; writing takes a grant</option>
        </select>
        <span class="text-xs text-text-muted">
          Applies to projects created from now on and to folders that appear on disk without settings.
          Each project's own visibility and grants are managed from Projects.
        </span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input
          v-model="draft.personal_projects"
          type="checkbox"
          class="mt-1"
          @change="save"
        />
        <span>
          <span class="block">Personal project for new accounts</span>
          <span class="text-xs text-text-muted">
            Every new User account gets a private project named after it where it is admin, so it
            has a place to work before anyone grants it anything. New accounts start
            <em>restricted</em> (they see only their grants) — lift it from Admin → Users.
          </span>
        </span>
      </label>
    </fieldset>

    <fieldset v-if="!auth.isOwner && !auth.isAnonymous" class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">My MCP tokens</legend>
      <MyTokens />
    </fieldset>

    <fieldset class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">Appearance</legend>
      <label class="block text-sm">
        <span class="text-text-muted">Theme preset</span>
        <select
          :value="ui.preset"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          @change="ui.setPreset(($event.target as HTMLSelectElement).value as ThemePreset)"
        >
          <option v-for="p in presetOptions" :key="p.value" :value="p.value">
            {{ p.label }} ({{ p.tone }})
          </option>
        </select>
      </label>
      <label class="block text-sm">
        <span class="text-text-muted">Language</span>
        <select
          :value="ui.locale"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          @change="ui.setLocale(($event.target as HTMLSelectElement).value as LocaleCode)"
        >
          <option v-for="l in enabledLocaleOptions" :key="l.value" :value="l.value">
            {{ l.label }}
          </option>
        </select>
      </label>
      <p class="text-xs text-text-muted">
        Theme + language are stored in your browser. Server-side `git`/`trash`/`i18n.default_lang`
        below configures the gosidian instance for everyone.
      </p>
    </fieldset>

    <p v-if="loading" class="text-text-muted">Loading…</p>
    <p v-else-if="error" class="text-danger">{{ error }}</p>

    <form
      v-else-if="data"
      class="space-y-8"
      @submit.prevent="save"
    >
      <p v-if="envOverrides.length" class="text-sm text-text-muted">
        Set by environment variables, so read-only here:
        <code class="font-mono text-xs">{{ envOverrides.join(', ') }}</code>.
      </p>
      <fieldset class="rounded border border-border bg-surface p-4 space-y-3">
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">Git sync</legend>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            :disabled="!auth.isOwner || fromEnv('git.enabled')"
            v-model="draft.git.enabled"
          />
          <span>Enabled</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">Remote</span>
          <input
            v-model.trim="draft.git.remote"
            :disabled="!auth.isOwner || fromEnv('git.remote')"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          />
        </label>
        <div class="grid grid-cols-2 gap-3">
          <label class="block text-sm">
            <span class="text-text-muted">Branch</span>
            <input
              v-model.trim="draft.git.branch"
              :disabled="!auth.isOwner || fromEnv('git.branch')"
              class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
            />
          </label>
          <label class="block text-sm">
            <span class="text-text-muted">Debounce (ms)</span>
            <input
              v-model.number="draft.git.debounce_ms"
              :disabled="!auth.isOwner || fromEnv('git.debounce_ms')"
              type="number"
              min="1000"
              class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
            />
          </label>
        </div>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            :disabled="!auth.isOwner || fromEnv('git.push')"
            v-model="draft.git.push"
          />
          <span>Push to remote on commit</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">Token env var name</span>
          <input
            v-model.trim="draft.git.token_env"
            :disabled="!auth.isOwner || fromEnv('git.token_env')"
            placeholder="GITEA_TOKEN"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 font-mono"
          />
        </label>
      </fieldset>

      <fieldset class="rounded border border-border bg-surface p-4 space-y-3">
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">Trash</legend>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            :disabled="!auth.isOwner || fromEnv('trash.enabled')"
            v-model="draft.trash.enabled"
          />
          <span>Enabled (soft-delete instead of hard-delete)</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">Retention (ms, 0 = forever)</span>
          <input
            v-model.number="draft.trash.retention_ms"
            :disabled="!auth.isOwner || fromEnv('trash.retention_ms')"
            type="number"
            min="0"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          />
        </label>
      </fieldset>

      <fieldset class="rounded border border-border bg-surface p-4 space-y-3">
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">i18n</legend>
        <div class="text-sm">
          <span class="text-text-muted">Languages offered in the selector</span>
          <div class="mt-1 flex flex-wrap gap-x-4 gap-y-1">
            <label v-for="l in localeOptions" :key="l.value" class="inline-flex items-center gap-2">
              <input
                v-model="draft.i18n.enabled_langs"
                type="checkbox"
                :value="l.value"
                :disabled="!auth.isOwner || fromEnv('i18n.enabled_langs')"
              />
              {{ l.label }}
            </label>
          </div>
        </div>
        <label class="block text-sm">
          <span class="text-text-muted">Default language</span>
          <select
            v-model="draft.i18n.default_lang"
            :disabled="!auth.isOwner || fromEnv('i18n.default_lang')"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          >
            <option v-for="l in defaultLangOptions" :key="l.value" :value="l.value">
              {{ l.label }}
            </option>
          </select>
        </label>
        <p class="text-xs text-text-muted">
          The default is the language a browser starts in; it must be one of the languages offered.
        </p>
      </fieldset>

      <div class="flex items-center gap-3">
        <button
          type="submit"
          :disabled="!auth.isOwner || saving"
          class="px-4 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover disabled:opacity-50"
        >{{ saving ? 'Saving…' : 'Save' }}</button>
        <p v-if="message" class="text-sm text-success">{{ message }}</p>
      </div>
    </form>
  </div>
</template>
