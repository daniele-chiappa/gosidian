import { describe, expect, it } from 'vitest'
import { AxiosError, AxiosHeaders } from 'axios'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import { describeError, errorText } from '@/api/errors'

function axiosError(status: number | null, message?: string, code = 'x'): AxiosError {
  const config = { headers: new AxiosHeaders() }
  const response =
    status === null
      ? undefined
      : { status, statusText: '', headers: {}, config, data: message ? { error: { code, message } } : {} }
  return new AxiosError(`Request failed with status code ${status}`, 'ERR', config, null, response)
}

const i18n = createI18n({ legacy: false, locale: 'it', messages: { en: enUI, it: itUI } })
const t = i18n.global.t as (k: string, v?: Record<string, unknown>) => string

describe('errorText (IMP-155, M3)', () => {
  it('says what kind of failure it is, in the user language, and the server detail under it', () => {
    expect(errorText(axiosError(403, 'project "x" is read-only'), t)).toBe('Non hai accesso.\nproject "x" is read-only')
    expect(errorText(axiosError(503), t)).toBe('Il server non è disponibile: riprova tra poco.')
    expect(errorText(axiosError(null), t)).toBe('Il server non risponde: controlla la connessione.')
  })

  it('names the action first when given', () => {
    expect(errorText(axiosError(404, 'note not found'), t, 'Salvataggio non riuscito')).toBe(
      'Salvataggio non riuscito: non trovato.\nnote not found',
    )
  })

  it('never shows "Request failed with status code"', () => {
    for (const s of [400, 401, 409, 412, 413, 418, 422, 429, 500, 502]) {
      expect(errorText(axiosError(s), t)).not.toMatch(/status code/)
    }
  })

  it('reads the status of an auth store error, and keeps a plain one as written', () => {
    const err = Object.assign(new Error('invalid credentials'), { status: 401 })
    expect(describeError(err, t)).toEqual({ summary: 'La sessione è finita: accedi di nuovo.', detail: 'invalid credentials' })
    expect(errorText(new Error('Le password non coincidono.'), t)).toBe('Le password non coincidono.')
  })

  it('reads a server code before the status: a wrong password is no missing access (review)', () => {
    expect(errorText(axiosError(403, 'wrong password', 'auth.invalid_credentials'), t, 'Cambio non riuscito')).toBe(
      'Cambio non riuscito: password o codice errati.\nwrong password',
    )
    expect(errorText(axiosError(403, 'owner only', 'auth.owner_only'), t)).toBe("Lo può fare solo l'admin.\nowner only")
  })
})
