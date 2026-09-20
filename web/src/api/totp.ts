import client from './client'

export interface AuthConfig {
  totp: boolean
  ldap: boolean
}

/** GET /api/v1/auth-config — public. Drives whether the LoginView renders the
 *  TOTP field (and, in Phase 3, the LDAP option). Booleans only. */
export async function getAuthConfig(): Promise<AuthConfig> {
  const { data } = await client.get<AuthConfig>('/auth-config')
  return data
}

export interface TotpEnrollData {
  secret: string
  otpauth_uri: string
  /** Self-contained inline SVG of the otpauth URI, rendered server-side. May be
   *  empty if QR rendering failed — fall back to the secret / URI. */
  qr_svg: string
}

/** Start enrolment: returns a fresh secret + otpauth URI (not yet active). */
export async function enrollTOTP(): Promise<TotpEnrollData> {
  const { data } = await client.post<TotpEnrollData>('/totp/enroll', {})
  return data
}

interface RecoveryCodesData {
  recovery_codes: string[]
}

/** Confirm a code against the candidate secret to activate it. Returns the
 *  account's first set of single-use recovery codes — shown once, the server
 *  keeps only their hashes. */
export async function confirmTOTP(secret: string, code: string): Promise<string[]> {
  const { data } = await client.post<RecoveryCodesData>('/totp/confirm', { secret, code })
  return data.recovery_codes
}

/** Replace every recovery code with a fresh set. Needs a current TOTP code:
 *  the session alone must not be able to mint itself a lasting second factor. */
export async function regenerateRecoveryCodes(code: string): Promise<string[]> {
  const { data } = await client.post<RecoveryCodesData>('/totp/recovery-codes', { code })
  return data.recovery_codes
}

/** Remove the current user's TOTP secret and recovery codes (403 if the
 *  policy requires it). */
export async function disenrollTOTP(): Promise<void> {
  await client.delete('/totp')
}
