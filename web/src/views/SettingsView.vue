<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { getSettings, updateSettings, type Settings } from '@/api/settings'
import { useAuthStore } from '@/stores/auth'
import { useUIStore, type LocaleCode, type ThemePreset } from '@/stores/ui'
import TotpEnroll from '@/components/domain/TotpEnroll.vue'
import RecoveryCodes from '@/components/domain/RecoveryCodes.vue'
import { disenrollTOTP, regenerateRecoveryCodes } from '@/api/totp'

const auth = useAuthStore()
const ui = useUIStore()

const totpError = ref<string | null>(null)
function onTotpEnrolled(codeCount: number) {
  auth.setEnrolled(true)
  auth.setRecoveryCodesRemaining(codeCount)
}
async function disableTotp() {
  totpError.value = null
  try {
    await disenrollTOTP()
    auth.setEnrolled(false)
  } catch (e) {
    totpError.value = e instanceof Error ? e.message : 'Failed to disable two-factor'
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
  i18n: { default_lang: string; enabled_langs: string }
  totp_mode: string
  default_visibility: string
}>({
  git: { enabled: false, remote: '', branch: '', debounce_ms: 30000, push: false, token_env: '' },
  trash: { enabled: false, retention_ms: 0 },
  i18n: { default_lang: 'en', enabled_langs: 'it,en' },
  totp_mode: 'off',
  default_visibility: 'private',
})
const loading = ref(false)
const saving = ref(false)
const message = ref<string | null>(null)
const error = ref<string | null>(null)

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
    enabled_langs: (s.i18n.enabled_langs ?? []).join(','),
  }
  draft.totp_mode = s.totp_mode ?? 'off'
  draft.default_visibility = s.default_visibility || 'private'
}

async function load() {
  loading.value = true
  error.value = null
  try {
    hydrate(await getSettings())
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load settings'
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  error.value = null
  message.value = null
  try {
    const result = await updateSettings({
      git: { ...draft.git },
      trash: { ...draft.trash },
      i18n: {
        default_lang: draft.i18n.default_lang,
        enabled_langs: draft.i18n.enabled_langs
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
      },
      totp_mode: draft.totp_mode,
      default_visibility: draft.default_visibility,
    })
    hydrate(result)
    message.value = 'Saved.'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Save failed'
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
      Read-only — only owners can change server settings.
    </p>

    <fieldset class="rounded border border-border bg-surface p-4 space-y-3 mb-6">
      <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">Two-factor (TOTP)</legend>
      <label v-if="auth.isOwner" class="block text-sm">
        <span class="text-text-muted">Global policy</span>
        <select
          v-model="draft.totp_mode"
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
          type="button"
          class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
          @click="disableTotp"
        >Disable two-factor</button>
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
          <option v-for="l in localeOptions" :key="l.value" :value="l.value">
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
      <fieldset class="rounded border border-border bg-surface p-4 space-y-3">
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">Git sync</legend>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            :disabled="!auth.isOwner"
            v-model="draft.git.enabled"
          />
          <span>Enabled</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">Remote</span>
          <input
            v-model.trim="draft.git.remote"
            :disabled="!auth.isOwner"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          />
        </label>
        <div class="grid grid-cols-2 gap-3">
          <label class="block text-sm">
            <span class="text-text-muted">Branch</span>
            <input
              v-model.trim="draft.git.branch"
              :disabled="!auth.isOwner"
              class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
            />
          </label>
          <label class="block text-sm">
            <span class="text-text-muted">Debounce (ms)</span>
            <input
              v-model.number="draft.git.debounce_ms"
              :disabled="!auth.isOwner"
              type="number"
              min="1000"
              class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
            />
          </label>
        </div>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            :disabled="!auth.isOwner"
            v-model="draft.git.push"
          />
          <span>Push to remote on commit</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">Token env var name</span>
          <input
            v-model.trim="draft.git.token_env"
            :disabled="!auth.isOwner"
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
            :disabled="!auth.isOwner"
            v-model="draft.trash.enabled"
          />
          <span>Enabled (soft-delete instead of hard-delete)</span>
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">Retention (ms, 0 = forever)</span>
          <input
            v-model.number="draft.trash.retention_ms"
            :disabled="!auth.isOwner"
            type="number"
            min="0"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
          />
        </label>
      </fieldset>

      <fieldset class="rounded border border-border bg-surface p-4 space-y-3">
        <legend class="px-2 text-sm uppercase tracking-wide text-text-muted">i18n</legend>
        <div class="grid grid-cols-2 gap-3">
          <label class="block text-sm">
            <span class="text-text-muted">Default language</span>
            <input
              v-model.trim="draft.i18n.default_lang"
              :disabled="!auth.isOwner"
              class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
            />
          </label>
          <label class="block text-sm">
            <span class="text-text-muted">Enabled (comma-separated)</span>
            <input
              v-model.trim="draft.i18n.enabled_langs"
              :disabled="!auth.isOwner"
              class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2"
            />
          </label>
        </div>
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
