/**
 * Pinia auth store. Holds the SPA Bearer token + the redacted user
 * view returned by /api/v1/login. Persisted via
 * pinia-plugin-persistedstate so a refresh keeps the user logged in.
 *
 * The actions deliberately do NOT use the `client` axios instance —
 * client.ts imports this store for the request interceptor, and a
 * back-import would create a cycle. Login/refresh use a tiny fetch
 * helper inline; the rest of the codebase uses the configured axios
 * client.
 */
import { defineStore } from 'pinia'

export type Role = 'owner' | 'member' | 'guest'

export interface User {
  id: string
  username: string
  role: Role
  totp_enrolled?: boolean
  /** Unused recovery codes; absent when two-factor is not enrolled. */
  recovery_codes_remaining?: number
}

interface AuthState {
  token: string
  expiresAt: string
  hardExpiry: string
  user: User | null
  /** Set when login reports the effective policy mandates TOTP but no secret
   *  is enrolled yet — the AppShell forces the enrolment interstitial. */
  enrollmentRequired: boolean
  /** Set when the login consumed a recovery code instead of a TOTP: the
   *  AppShell shows a one-time notice until dismissed (persisted, so a reload
   *  does not swallow it). */
  recoveryCodeUsed: boolean
  /** True when the server runs read-only anonymous access (open mode) and we
   *  have no token: the shell renders read-only as a guest. Derived from
   *  /version at boot; deliberately NOT persisted. See BUG-018. */
  openMode: boolean
}

interface LoginResponse {
  token: string
  expires_at: string
  hard_expiry: string
  user: User
  totp_enrollment_required?: boolean
  recovery_code_used?: boolean
}

interface RefreshResponse {
  token: string
  expires_at: string
  hard_expiry: string
}

interface ErrorBody {
  error?: { code?: string; message?: string }
}

async function postJson<T>(path: string, body: object, token?: string): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = `Bearer ${token}`
  const res = await fetch(`/api/v1${path}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as ErrorBody
    throw new Error(data.error?.message ?? `HTTP ${res.status}`)
  }
  return (await res.json()) as T
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    token: '',
    expiresAt: '',
    hardExpiry: '',
    user: null,
    enrollmentRequired: false,
    recoveryCodeUsed: false,
    openMode: false,
  }),

  getters: {
    isAuthenticated: (s) => Boolean(s.token && s.user),
    /** Token-less read-only guest session admitted by the server's open mode. */
    isAnonymous: (s) => s.openMode && !s.token && Boolean(s.user),
    isOwner: (s) => s.user?.role === 'owner',
    isGuest: (s) => s.user?.role === 'guest',
    /** owner or member — may create/edit/delete. Guests are read-only. */
    canWrite: (s) => s.user?.role === 'owner' || s.user?.role === 'member',
    username: (s) => s.user?.username ?? '',
  },

  actions: {
    async login(username: string, password: string, totp?: string) {
      const body: { username: string; password: string; totp?: string } = { username, password }
      if (totp) body.totp = totp
      const data = await postJson<LoginResponse>('/login', body)
      this.token = data.token
      this.expiresAt = data.expires_at
      this.hardExpiry = data.hard_expiry
      this.user = data.user
      this.enrollmentRequired = Boolean(data.totp_enrollment_required)
      this.recoveryCodeUsed = Boolean(data.recovery_code_used)
      this.openMode = false // a real session supersedes any anonymous open-mode state
    },

    clearEnrollment() {
      this.enrollmentRequired = false
    },

    dismissRecoveryNotice() {
      this.recoveryCodeUsed = false
    },

    /** Track the recovery-code counter after an enrolment or a regeneration. */
    setRecoveryCodesRemaining(n: number) {
      if (this.user) this.user.recovery_codes_remaining = n
    },

    /** Raise the enrolment interstitial. Called by the API client when any
     *  route returns 403 auth.enrollment_required — e.g. an owner flipped the
     *  global TOTP policy to "required" while this session was live. */
    requireEnrollment() {
      this.enrollmentRequired = true
    },

    setEnrolled(v: boolean) {
      if (!this.user) return
      this.user.totp_enrolled = v
      if (!v) this.user.recovery_codes_remaining = undefined
    },

    async refresh() {
      if (!this.token) return
      const data = await postJson<RefreshResponse>('/refresh', {}, this.token)
      this.expiresAt = data.expires_at
      this.hardExpiry = data.hard_expiry
    },

    async logout() {
      if (this.token) {
        // Best-effort: a network failure on logout still clears
        // local state so the SPA never deadlocks an unrecoverable
        // session.
        await postJson<unknown>('/logout', {}, this.token).catch(() => {})
      }
      this.clear()
    },

    clear() {
      this.token = ''
      this.expiresAt = ''
      this.hardExpiry = ''
      this.user = null
      this.enrollmentRequired = false
      this.recoveryCodeUsed = false
      this.openMode = false
    },

    /** Establish a token-less, read-only guest session for servers running
     *  GOSIDIAN_OPEN_MODE=readonly. Called at boot from /version. The guest
     *  role makes canWrite false and the RBAC limits reads to public projects;
     *  the server independently injects the same guest principal (BUG-018). */
    setAnonymous() {
      this.token = ''
      this.expiresAt = ''
      this.hardExpiry = ''
      this.user = { id: 'anonymous', username: 'guest', role: 'guest' }
      this.enrollmentRequired = false
      this.recoveryCodeUsed = false
      this.openMode = true
    },
  },

  persist: {
    key: 'gosidian.auth',
    storage: localStorage,
    paths: ['token', 'expiresAt', 'hardExpiry', 'user', 'enrollmentRequired', 'recoveryCodeUsed'],
  },
})
