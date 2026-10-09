<script setup lang="ts">
/**
 * LoginView — the sign-in form, and the sign-up form of an invite link
 * (`/login?invite=<token>`, minted in Admin → Invites): the invitee picks
 * a username and a password, the account is created as a member, and the
 * sign-in form comes back with the username filled in (BUG-099). An invite
 * expired or used says so, and asks for a new link.
 */
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { noteSignedIn, withoutWorkspace } from '@/composables/useSessionReset'
import { getAuthConfig } from '@/api/totp'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { isInvalidInvite, signup } from '@/api/signup'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const { t } = useI18n()

const username = ref('')
const password = ref('')
const totp = ref('')
const showTotp = ref(false)
const ldapEnabled = ref(false)
const error = ref<string | null>(null)
const submitting = ref(false)

// --- Sign-up from an invite (BUG-099) ---
const invite = ref(typeof route.query.invite === 'string' ? route.query.invite : '')
const signupPassword = ref('')
const signupConfirm = ref('')
const inviteInvalid = ref(false)
const notice = ref<string | null>(null)

function backToLogin() {
  invite.value = ''
  inviteInvalid.value = false
  error.value = null
  const query = { ...route.query }
  delete query.invite
  void router.replace({ path: '/login', query })
}

async function handleSignup() {
  if (submitting.value) return
  error.value = null
  if (signupPassword.value.length < 8) {
    error.value = t('signup.password_short')
    return
  }
  if (signupPassword.value !== signupConfirm.value) {
    error.value = t('signup.password_mismatch')
    return
  }
  submitting.value = true
  try {
    await signup(username.value, signupPassword.value, invite.value)
    signupPassword.value = ''
    signupConfirm.value = ''
    password.value = ''
    backToLogin()
    notice.value = t('signup.created')
  } catch (e) {
    if (isInvalidInvite(e)) inviteInvalid.value = true
    else error.value = errorText(e, t, t('signup.failed'))
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  try {
    const cfg = await getAuthConfig()
    showTotp.value = cfg.totp
    ldapEnabled.value = cfg.ldap
  } catch {
    showTotp.value = false
  }
})

const nextTarget = computed(() => {
  const raw = route.query.next
  if (typeof raw === 'string' && raw.startsWith('/')) return raw
  return '/'
})

async function handleSubmit() {
  if (submitting.value) return
  submitting.value = true
  error.value = null
  try {
    await auth.login(username.value, password.value, totp.value || undefined)
    // Not the account last seen in this browser, or none known: start clean,
    // without the windows of whoever the address came from.
    const same = noteSignedIn(auth.user?.id ?? '')
    await router.push(same ? nextTarget.value : withoutWorkspace(nextTarget.value))
  } catch (e) {
    // A 401 here is the credentials, not a session that ended.
    const status = (e as { status?: number } | null)?.status
    error.value = status === 401 ? t('login.bad_credentials') : errorText(e, t, t('login.failed'))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-bg text-text px-4">
    <div v-if="invite" class="w-full max-w-sm" data-signup>
      <div class="mb-6 text-center">
        <h1 class="text-2xl font-semibold">gosidian</h1>
        <p class="text-sm text-text-muted">{{ t('signup.title') }}</p>
      </div>

      <div v-if="inviteInvalid" class="space-y-3 rounded-lg bg-surface p-5 shadow ring-1 ring-border">
        <p class="text-sm text-danger" role="alert">{{ t('signup.invalid_invite') }}</p>
        <button
          type="button"
          class="w-full rounded border border-border py-2 font-medium hover:bg-bg-elevated"
          @click="backToLogin"
        >
          {{ t('signup.back_to_login') }}
        </button>
      </div>

      <form
        v-else
        class="space-y-3 rounded-lg bg-surface p-5 shadow ring-1 ring-border"
        @submit.prevent="handleSignup"
      >
        <p class="text-sm text-text-muted">{{ t('signup.subtitle') }}</p>
        <label class="block text-sm">
          <span class="text-text-muted">{{ t('signup.username') }}</span>
          <input
            v-model.trim="username"
            type="text"
            autocomplete="username"
            required
            autofocus
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">{{ t('signup.password') }}</span>
          <input
            v-model="signupPassword"
            type="password"
            autocomplete="new-password"
            required
            data-signup-password
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>
        <label class="block text-sm">
          <span class="text-text-muted">{{ t('signup.password_confirm') }}</span>
          <input
            v-model="signupConfirm"
            type="password"
            autocomplete="new-password"
            required
            data-signup-confirm
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>

        <ErrorMessage v-if="error" :text="error" class="text-sm" />

        <button
          type="submit"
          :disabled="submitting"
          class="w-full rounded bg-accent text-accent-fg py-2 font-medium hover:bg-accent-hover disabled:opacity-60"
        >
          {{ submitting ? t('signup.creating') : t('signup.create_account_button') }}
        </button>
        <button
          type="button"
          class="w-full text-xs text-text-muted hover:text-text"
          @click="backToLogin"
        >
          {{ t('signup.back_to_login') }}
        </button>
      </form>
    </div>

    <div v-else class="w-full max-w-sm">
      <div class="mb-6 text-center">
        <h1 class="text-2xl font-semibold">gosidian</h1>
        <p class="text-sm text-text-muted">{{ t('login.subtitle') }}</p>
        <p v-if="ldapEnabled" class="mt-1 text-xs text-text-muted">
          {{ t('login.ldap_hint') }}
        </p>
      </div>

      <form
        class="space-y-3 rounded-lg bg-surface p-5 shadow ring-1 ring-border"
        @submit.prevent="handleSubmit"
      >
        <label class="block text-sm">
          <span class="text-text-muted">{{ t('common.username') }}</span>
          <input
            v-model.trim="username"
            type="text"
            autocomplete="username"
            required
            autofocus
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>

        <label class="block text-sm">
          <span class="text-text-muted">{{ t('common.password') }}</span>
          <input
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>

        <label v-if="showTotp" class="block text-sm">
          <span class="text-text-muted">
            {{ t('login.totp') }}
            <span class="opacity-60">{{ t('login.totp_hint') }}</span>
          </span>
          <!-- inputmode "text", not "numeric": recovery codes carry letters. -->
          <input
            v-model.trim="totp"
            type="text"
            inputmode="text"
            autocomplete="one-time-code"
            autocapitalize="characters"
            :placeholder="t('login.totp_placeholder')"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>

        <p v-if="notice && !error" class="text-sm text-success" role="status" data-signup-done>{{ notice }}</p>
        <ErrorMessage v-if="error" :text="error" class="text-sm" />

        <button
          type="submit"
          :disabled="submitting"
          class="w-full rounded bg-accent text-accent-fg py-2 font-medium hover:bg-accent-hover disabled:opacity-60"
        >
          {{ submitting ? t('login.submitting') : t('login.submit') }}
        </button>
      </form>
    </div>
  </div>
</template>
