<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSettings, updateSettings, type Settings, type UpdateSettings } from '@/api/settings'
import PasswordChange from '@/components/domain/PasswordChange.vue'
import { useAuthStore } from '@/stores/auth'
import { useUIStore, type LocaleCode, type ThemePreset } from '@/stores/ui'
import TotpEnroll from '@/components/domain/TotpEnroll.vue'
import MyTokens from '@/components/domain/MyTokens.vue'
import RecoveryCodes from '@/components/domain/RecoveryCodes.vue'
import { disenrollTOTP, regenerateRecoveryCodes } from '@/api/totp'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { visibilityHelp, visibilityLabel } from '@/api/access'

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
    totpError.value = errorText(e, t, t('settings_view.totp_disable_failed'))
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
    totpError.value = errorText(e, t, t('settings_view.regen_failed'))
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
  { value: 'custom', label: 'Custom (Mocha)', tone: 'dark' },
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
    error.value = errorText(e, t, t('settings_view.load_failed'))
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
    message.value = t('settings_view.saved')
    // The languages apply at once: the selector above follows them.
    await ui.refreshLocales()
  } catch (e) {
    error.value = errorText(e, t, t('note.save_failed'))
  } finally {
    saving.value = false
  }
}

// The owner controls that save on change send their own field alone
// (BUG-109): the whole draft carried the form's half-made edits below, or,
// before the settings had loaded, its placeholders (git and trash off,
// retention 0, no languages), and the server saved them. The form's
// fields keep what is typed in them. While a save is on its way they are
// disabled: its reply would put back the value they replace.
type OwnerToggle = Pick<UpdateSettings, 'totp_mode' | 'default_visibility' | 'personal_projects'>
async function saveToggle(patch: OwnerToggle) {
  if (!data.value || saving.value) return
  saving.value = true
  error.value = null
  message.value = null
  try {
    data.value = await updateSettings(withoutEnvFields(patch))
    message.value = t('settings_view.saved')
  } catch (e) {
    error.value = errorText(e, t, t('note.save_failed'))
    // The control shows the saved value again, not the one refused.
    const s = data.value
    if (s && 'totp_mode' in patch) draft.totp_mode = s.totp_mode ?? 'off'
    if (s && 'default_visibility' in patch) draft.default_visibility = s.default_visibility || 'private'
    if (s && 'personal_projects' in patch) draft.personal_projects = s.personal_projects ?? true
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="p-8 max-w-3xl mx-auto">
    <h1 class="text-2xl font-semibold mb-1">{{ t('settings_view.title') }}</h1>
    <p
      v-if="!auth.isOwner"
      class="text-sm text-text-muted mb-6"
    >
      {{ t('settings_view.read_only') }}
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
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">{{ t('settings_view.totp') }}</legend>
      <label v-if="auth.isOwner && data" class="block text-sm">
        <span class="text-text-muted">{{ t('settings_view.totp_policy') }}</span>
        <select
          v-model="draft.totp_mode"
          :disabled="saving || fromEnv('totp_mode')"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          @change="saveToggle({ totp_mode: draft.totp_mode })"
        >
          <option value="off">{{ t('settings_view.totp_off') }}</option>
          <option value="optional">{{ t('settings_view.totp_optional') }}</option>
          <option value="required">{{ t('settings_view.totp_required') }}</option>
        </select>
        <span class="text-xs text-text-muted">{{ t('settings_view.totp_per_user') }}</span>
      </label>
      <hr v-if="auth.isOwner && data" class="border-border" />
      <template v-if="auth.user?.totp_enrolled">
        <p class="text-sm text-success">{{ t('settings_view.totp_on') }}</p>
        <p class="text-sm text-text-muted">
          {{ t('settings_view.recovery_left') }}
          <span class="font-medium text-text">{{ auth.user?.recovery_codes_remaining ?? 0 }}</span>
          <span v-if="(auth.user?.recovery_codes_remaining ?? 0) <= 2" class="text-warning">
            — {{ t('settings_view.recovery_low') }}
          </span>
        </p>
        <RecoveryCodes v-if="regenCodes.length" :codes="regenCodes" @done="regenCodes = []" />
        <template v-else>
          <button
            v-if="!regenOpen"
            type="button"
            class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
            @click="regenOpen = true"
          >{{ t('settings_view.regen_open') }}</button>
          <div v-else class="space-y-2">
            <p class="text-xs text-text-muted">
              {{ t('settings_view.regen_hint') }}
            </p>
            <div class="flex gap-2">
              <input
                v-model.trim="regenCode"
                inputmode="numeric"
                autocomplete="one-time-code"
                placeholder="123 456"
                class="w-40 rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
                @keyup.enter="regenerate"
              />
              <button
                type="button"
                :disabled="regenBusy || !regenCode"
                class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
                @click="regenerate"
              >{{ t('settings_view.regen') }}</button>
              <button
                type="button"
                class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
                @click="cancelRegen"
              >{{ t('common.cancel') }}</button>
            </div>
          </div>
        </template>
        <button
          v-if="!disableOpen"
          type="button"
          class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
          @click="disableOpen = true"
        >{{ t('settings_view.totp_disable_open') }}</button>
        <div v-else class="flex flex-wrap items-end gap-2" data-totp-disable>
          <label class="block text-sm">
            <span class="text-text-muted">{{ t('password.confirm_action') }}</span>
            <input
              v-model="disablePassword"
              type="password"
              autocomplete="current-password"
              class="mt-1 w-56 rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
              @keyup.enter="disableTotp"
            />
          </label>
          <button
            type="button"
            :disabled="!disablePassword"
            class="rounded bg-danger text-white px-3 py-2 text-sm disabled:opacity-60"
            @click="disableTotp"
          >{{ t('settings_view.totp_disable') }}</button>
          <button
            type="button"
            class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
            @click="disableOpen = false; disablePassword = ''"
          >{{ t('common.cancel') }}</button>
        </div>
        <ErrorMessage v-if="totpError" :text="totpError" class="text-sm" />
      </template>
      <TotpEnroll v-else @done="onTotpEnrolled" />
    </fieldset>

    <fieldset v-if="auth.isOwner && data" class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">{{ t('settings_view.project_access') }}</legend>
      <label class="block text-sm">
        <span class="text-text-muted">{{ t('settings_view.default_visibility') }}</span>
        <select
          v-model="draft.default_visibility"
          :disabled="saving"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          @change="saveToggle({ default_visibility: draft.default_visibility })"
        >
          <option value="private">{{ visibilityLabel('private') }}</option>
          <option value="internal">{{ visibilityLabel('internal') }}</option>
          <option value="public">{{ visibilityLabel('public') }}</option>
        </select>
        <span class="mt-1 block text-xs text-text-muted">{{ visibilityHelp(draft.default_visibility) }}</span>
        <span class="text-xs text-text-muted">
          {{ t('settings_view.default_visibility_hint') }}
        </span>
      </label>
      <label class="flex items-start gap-2 text-sm">
        <input
          v-model="draft.personal_projects"
          type="checkbox"
          :disabled="saving"
          class="mt-1"
          @change="saveToggle({ personal_projects: draft.personal_projects })"
        />
        <span>
          <span class="block">{{ t('settings_view.personal') }}</span>
          <span class="text-xs text-text-muted">
            {{ t('settings_view.personal_hint') }}
          </span>
        </span>
      </label>
    </fieldset>

    <fieldset v-if="!auth.isOwner && !auth.isAnonymous" class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">{{ t('settings_view.my_tokens') }}</legend>
      <MyTokens />
    </fieldset>

    <fieldset class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">{{ t('settings_view.appearance') }}</legend>
      <label class="block text-sm">
        <span class="text-text-muted">{{ t('settings_view.theme') }}</span>
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
        <span class="text-text-muted">{{ t('settings_view.language') }}</span>
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
        {{ t('settings_view.appearance_hint') }}
      </p>
    </fieldset>

    <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-else-if="error" :text="error" />

    <form
      v-else-if="data"
      class="space-y-8"
      @submit.prevent="save"
    >
      <p v-if="envOverrides.length" class="text-sm text-text-muted">
        {{ t('settings_view.env_overrides') }}
        <code class="font-mono text-xs">{{ envOverrides.join(', ') }}</code>.
      </p>
      <fieldset class="rounded border border-border bg-surface p-4 space-y-3">
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">{{ t('settings_view.git') }}</legend>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            :disabled="!auth.isOwner || fromEnv('git.enabled')"
            v-model="draft.git.enabled"
          />
          <span>{{ t('settings_view.enabled') }}</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">{{ t('settings_view.remote') }}</span>
          <input
            v-model.trim="draft.git.remote"
            :disabled="!auth.isOwner || fromEnv('git.remote')"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          />
        </label>
        <div class="grid grid-cols-2 gap-3">
          <label class="block text-sm">
            <span class="text-text-muted">{{ t('settings_view.branch') }}</span>
            <input
              v-model.trim="draft.git.branch"
              :disabled="!auth.isOwner || fromEnv('git.branch')"
              class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
            />
          </label>
          <label class="block text-sm">
            <span class="text-text-muted">{{ t('settings_view.debounce') }}</span>
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
          <span>{{ t('settings_view.push') }}</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">{{ t('settings_view.token_env') }}</span>
          <input
            v-model.trim="draft.git.token_env"
            :disabled="!auth.isOwner || fromEnv('git.token_env')"
            placeholder="GITEA_TOKEN"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 font-mono"
          />
        </label>
      </fieldset>

      <fieldset class="rounded border border-border bg-surface p-4 space-y-3">
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">{{ t('trash.title') }}</legend>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            :disabled="!auth.isOwner || fromEnv('trash.enabled')"
            v-model="draft.trash.enabled"
          />
          <span>{{ t('settings_view.trash_enabled') }}</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">{{ t('settings_view.retention') }}</span>
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
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">{{ t('settings_view.languages') }}</legend>
        <div class="text-sm">
          <span class="text-text-muted">{{ t('settings_view.languages_offered') }}</span>
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
          <span class="text-text-muted">{{ t('settings_view.default_language') }}</span>
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
          {{ t('settings_view.default_language_hint') }}
        </p>
      </fieldset>

      <div class="flex items-center gap-3">
        <button
          type="submit"
          :disabled="!auth.isOwner || saving"
          class="px-4 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover disabled:opacity-50"
        >{{ saving ? t('note.saving') : t('common.save') }}</button>
        <p v-if="message" class="text-sm text-success">{{ message }}</p>
      </div>
    </form>
  </div>
</template>
