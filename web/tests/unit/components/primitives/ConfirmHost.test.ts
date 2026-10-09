import { afterEach, describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import ConfirmHost from '@/components/primitives/ConfirmHost.vue'
import { askText, cancelAll, confirmAction, showNotice } from '@/composables/useConfirm'

function mountHost() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(ConfirmHost, { global: { plugins: [i18n] }, attachTo: document.body })
}
// PlanciaModal teleports to <body>.
const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]')
const button = (text: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.trim() === text)

describe('ConfirmHost', () => {
  afterEach(() => cancelAll())

  it('asks in its own dialog, with the confirm button focused, and answers', async () => {
    const w = mountHost()
    const answer = confirmAction('Delete p/a.md?', { confirmLabel: 'Delete' })
    await flushPromises()
    expect(dialog()?.textContent).toContain('Delete p/a.md?')
    expect(dialog()?.textContent).toContain('Are you sure?')
    expect(document.activeElement).toBe(button('Delete'))
    button('Delete')!.click()
    expect(await answer).toBe(true)
    await flushPromises()
    expect(dialog()).toBeNull()
    w.unmount()
  })

  it('takes Cancel and Esc for a no', async () => {
    const w = mountHost()
    const first = confirmAction('Revoke it?')
    await flushPromises()
    button('Cancel')!.click()
    expect(await first).toBe(false)

    const second = confirmAction('Revoke it?')
    await flushPromises()
    document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(await second).toBe(false)
    w.unmount()
  })

  it('shows one request at a time, in order', async () => {
    const w = mountHost()
    const a = confirmAction('First?')
    const b = confirmAction('Second?')
    await flushPromises()
    expect(document.body.querySelectorAll('[role="dialog"]')).toHaveLength(1)
    expect(dialog()?.textContent).toContain('First?')
    button('Confirm')!.click()
    expect(await a).toBe(true)
    await flushPromises()
    expect(dialog()?.textContent).toContain('Second?')
    button('Cancel')!.click()
    expect(await b).toBe(false)
    w.unmount()
  })

  it('asks for a text, from the field, and gives null when cancelled', async () => {
    const w = mountHost()
    const named = askText('New name for alpha', 'alpha', { confirmLabel: 'Rename' })
    await flushPromises()
    // A question is titled by what it does, not "Are you sure?".
    expect(document.body.querySelector('.plancia-modal__title')?.textContent).toBe('Rename')
    const input = document.body.querySelector<HTMLInputElement>('[data-dialog-input]')!
    expect(document.activeElement).toBe(input)
    // Enter while an input method composes is part of the typing.
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, isComposing: true }))
    await flushPromises()
    expect(dialog()).not.toBeNull()
    expect(input.value).toBe('alpha')
    input.value = 'beta'
    input.dispatchEvent(new Event('input'))
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    expect(await named).toBe('beta')

    const cancelled = askText('New name for beta', 'beta')
    await flushPromises()
    button('Cancel')!.click()
    expect(await cancelled).toBeNull()
    w.unmount()
  })

  it('shows a notice with one button', async () => {
    const w = mountHost()
    const seen = showNotice('The note is locked')
    await flushPromises()
    expect(dialog()?.textContent).toContain('Something went wrong')
    expect(button('Cancel')).toBeUndefined()
    button('OK')!.click()
    await expect(seen).resolves.toBeUndefined()
    w.unmount()
  })
})
