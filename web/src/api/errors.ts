import axios from 'axios'

/**
 * What a failed request says to the user (IMP-155, M3): a sentence for the
 * kind of failure in the user's language, and under it the server's own
 * message, which stays in English. "Request failed with status code 404"
 * showed in 47 places.
 */

type T = (key: string, values?: Record<string, unknown>) => string

const STATUS_KEY: Record<number, string> = {
  400: 'bad_request',
  401: 'unauthorized',
  403: 'forbidden',
  404: 'not_found',
  409: 'conflict',
  412: 'changed',
  413: 'too_large',
  422: 'invalid',
  429: 'rate_limited',
  500: 'server',
  502: 'unavailable',
  503: 'unavailable',
  504: 'unavailable',
}

// Server codes that say more than their status: a wrong password answers
// 403 like a missing grant, and "you do not have access" would mislead.
const CODE_KEY: Record<string, string> = {
  'auth.invalid_credentials': 'wrong_password',
  'auth.owner_only': 'owner_only',
  'auth.password_change_required': 'password_change',
  'auth.enrollment_required': 'enrollment',
  'auth.invite_invalid': 'invite_invalid',
}

/** Every catalogue key describeError may name, for the catalogue test. */
export const ERROR_KEYS = [
  ...new Set([...Object.values(STATUS_KEY), ...Object.values(CODE_KEY), 'network', 'unknown']),
].map((k) => `request_error.${k}`)

/** An error the auth store throws for a failed fetch: the status rides along. */
export interface StatusError extends Error {
  status?: number
  code?: string
}

function serverMessage(data: unknown): string {
  const msg = (data as { error?: { message?: unknown } } | undefined)?.error?.message
  return typeof msg === 'string' ? msg : ''
}

function serverCode(data: unknown): string {
  const code = (data as { error?: { code?: unknown } } | undefined)?.error?.code
  return typeof code === 'string' ? code : ''
}

/** The catalogue key for a failure: its server code first, else its status. */
function keyFor(status: number, code: string): string {
  return CODE_KEY[code] ?? STATUS_KEY[status] ?? (status >= 500 ? 'server' : 'bad_request')
}

/** The sentence for the failure and the server's detail, "" when there is none. */
export function describeError(e: unknown, t: T): { summary: string; detail: string } {
  if (axios.isAxiosError(e)) {
    if (!e.response) return { summary: t('request_error.network'), detail: '' }
    const key = keyFor(e.response.status, serverCode(e.response.data))
    return { summary: t(`request_error.${key}`), detail: serverMessage(e.response.data) }
  }
  const status = (e as StatusError | null)?.status
  if (typeof status === 'number') {
    const key = keyFor(status, (e as StatusError).code ?? '')
    return { summary: t(`request_error.${key}`), detail: (e as Error).message }
  }
  // Not a request: a message of the web UI itself.
  return { summary: e instanceof Error && e.message ? e.message : t('request_error.unknown'), detail: '' }
}

const lowerFirst = (s: string) => s.charAt(0).toLocaleLowerCase() + s.slice(1)

/**
 * errorText is what to show for e: "<action>: <why>" when an action is
 * given ("Save failed: you do not have access."), the server's message on a
 * second line. ErrorMessage shows the two lines.
 */
export function errorText(e: unknown, t: T, action?: string): string {
  const { summary, detail } = describeError(e, t)
  const head = action ? `${action}: ${lowerFirst(summary)}` : summary
  return detail && detail !== summary ? `${head}\n${detail}` : head
}
