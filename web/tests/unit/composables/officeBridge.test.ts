import { describe, expect, it, vi } from 'vitest'
import { createOfficeBridge, OFFICE_BRIDGE, openable, type OfficeBridgeDeps } from '@/composables/officeBridge'

// The window's side of the bridge office/1 (IMP-233): the office's page in
// the frame says ready and asks; the window reads the team with the API and
// opens the notes the page names.

const ORIGIN = 'https://office.example.com'
const TEAM = { path: 'fitness/squadra.md', title: 'squadra', team: { roles: [] }, roles: {} }

function setup(over: Partial<OfficeBridgeDeps> = {}) {
  const posted: { msg: Record<string, unknown>; origin: string }[] = []
  const frame = {
    postMessage: (msg: unknown, origin: string) => posted.push({ msg: msg as Record<string, unknown>, origin }),
  }
  const deps: OfficeBridgeDeps = {
    path: 'fitness/squadra.md',
    origin: ORIGIN,
    frame: () => frame,
    read: vi.fn(async () => TEAM),
    open: vi.fn(),
    theme: () => 'dark',
    lang: () => 'it',
    errorText: (e) => String((e as Error).message),
    ...over,
  }
  const bridge = createOfficeBridge(deps)
  const from = (data: Record<string, unknown>, origin = ORIGIN, source: unknown = frame) =>
    bridge.handle({ data: { bridge: OFFICE_BRIDGE, ...data }, origin, source })
  return { bridge, deps, posted, frame, from }
}

const settle = () => new Promise((r) => setTimeout(r, 0))

describe('the bridge office/1', () => {
  it('hands the team to the page once it is ready, with the theme and the language', async () => {
    const { bridge, deps, posted, from } = setup()
    await bridge.refresh()
    expect(deps.read).not.toHaveBeenCalled()
    expect(from({ type: 'ready' })).toBe(true)
    await vi.waitFor(() => expect(posted).toHaveLength(1))
    expect(deps.read).toHaveBeenCalledWith('fitness/squadra.md')
    expect(posted[0]).toEqual({
      msg: { bridge: OFFICE_BRIDGE, type: 'team', team: TEAM, theme: 'dark', lang: 'it' },
      origin: ORIGIN,
    })
  })

  it('takes messages only from its frame, from the app origin, of its bridge', async () => {
    const { bridge, deps, frame } = setup()
    expect(bridge.handle({ data: { bridge: OFFICE_BRIDGE, type: 'ready' }, origin: 'https://evil.example', source: frame })).toBe(false)
    expect(bridge.handle({ data: { bridge: OFFICE_BRIDGE, type: 'ready' }, origin: ORIGIN, source: {} })).toBe(false)
    expect(bridge.handle({ data: { bridge: 'layout/2', type: 'ready' }, origin: ORIGIN, source: frame })).toBe(false)
    expect(bridge.handle({ data: 'ready', origin: ORIGIN, source: frame })).toBe(false)
    await settle()
    expect(bridge.ready).toBe(false)
    expect(deps.read).not.toHaveBeenCalled()
  })

  it('opens only the notes of the team project', () => {
    const { deps, from } = setup()
    from({ type: 'open', path: 'fitness/tasks/20261118-090000-app.md' })
    from({ type: 'open', path: 'altro/segreto.md' })
    from({ type: 'open', path: 'fitness/../altro/segreto.md' })
    from({ type: 'open', path: 'fitness/attachments/foto.png' })
    from({ type: 'open', path: 42 })
    expect(deps.open).toHaveBeenCalledTimes(1)
    expect(deps.open).toHaveBeenCalledWith('fitness/tasks/20261118-090000-app.md')
  })

  it('answers a refresh, and the asks during a read make one more read, not one each', async () => {
    let release: () => void = () => {}
    const read = vi.fn(
      () =>
        new Promise<unknown>((r) => {
          release = () => r(TEAM)
        }),
    )
    const { posted, from } = setup({ read })
    from({ type: 'ready' })
    from({ type: 'refresh' })
    from({ type: 'refresh' })
    await settle()
    expect(read).toHaveBeenCalledTimes(1)
    release()
    await vi.waitFor(() => expect(read).toHaveBeenCalledTimes(2))
    release()
    await vi.waitFor(() => expect(posted).toHaveLength(2))
    await settle()
    expect(read).toHaveBeenCalledTimes(2)
  })

  it('tells the page when the team cannot be read', async () => {
    const { posted, from } = setup({ read: vi.fn(async () => Promise.reject(new Error('note not found'))) })
    from({ type: 'ready' })
    await vi.waitFor(() => expect(posted).toHaveLength(1))
    expect(posted[0]!.msg).toEqual({ bridge: OFFICE_BRIDGE, type: 'error', message: 'note not found' })
  })

  it('waits for a new ready after the page is reloaded', async () => {
    const { bridge, deps, from } = setup()
    from({ type: 'ready' })
    await vi.waitFor(() => expect(deps.read).toHaveBeenCalledTimes(1))
    bridge.forget()
    await bridge.refresh()
    expect(deps.read).toHaveBeenCalledTimes(1)
  })
})

// Phase C: the page's changes are a draft the window keeps and the server
// checks; only the window saves it.
describe('the draft of the bridge office/1', () => {
  const EDITABLE = { ...TEAM, etag: 'e1', editable: true }
  const answer = (over: Record<string, unknown> = {}) => ({
    view: { path: 'fitness/squadra.md', team: { roles: [] }, roles: {} },
    block: 'roles: {}\n',
    line: 8,
    etag: 'e1',
    changed: false,
    creates: [],
    problems: [],
    saved: false,
    palette: { profiles: [] },
    ...over,
  })
  const refusal = (op: number, message: string) =>
    Object.assign(new Error(message), { response: { status: 422, data: { error: { message, details: { op } } } } })

  async function editing(over: Partial<OfficeBridgeDeps> = {}) {
    const edit = vi.fn(async (_p: string, body: { ops: unknown[]; save: boolean }) =>
      answer({ saved: body.save, etag: body.save ? 'e2' : 'e1' }),
    )
    const onDraft = vi.fn()
    const s = setup({ read: vi.fn(async () => EDITABLE), edit, onDraft, ...over })
    s.from({ type: 'ready' })
    await vi.waitFor(() => expect(s.posted).toHaveLength(1))
    s.from({ type: 'editing', on: true })
    await vi.waitFor(() => expect(s.posted).toHaveLength(2))
    return { ...s, edit: (over.edit as typeof edit) ?? edit, onDraft }
  }

  it('checks the draft on the version it read, and hands the page the team as it makes it', async () => {
    const { edit, posted, from, onDraft } = await editing()
    expect(edit).toHaveBeenLastCalledWith('fitness/squadra.md', { etag: 'e1', ops: [], save: false })
    expect(posted[1]!.msg).toMatchObject({ type: 'team', team: answer().view, draft: { editing: true, ops: 0, rebased: false, line: 8, palette: { profiles: [] } } })
    const op = { op: 'set', role: 'dev', field: 'model', value: 'big' }
    from({ type: 'edit', op })
    await vi.waitFor(() => expect(posted).toHaveLength(3))
    expect(edit).toHaveBeenLastCalledWith('fitness/squadra.md', { etag: 'e1', ops: [op], save: false })
    expect(onDraft).toHaveBeenLastCalledWith({ ops: 1, saving: false, savedAt: 0, error: '' })
    // Not a change: nothing.
    from({ type: 'edit', op: 'set' })
    from({ type: 'edit', op: { op: 'DROP TABLE' } })
    from({ type: 'edit', op: [1] })
    await settle()
    expect(edit).toHaveBeenCalledTimes(2)
  })

  it('takes away the change the server refuses, and says why', async () => {
    let refuse = true
    const edit = vi.fn(async (_p: string, body: { ops: { op: string }[] }) => {
      if (refuse && body.ops.length === 2) throw refusal(1, 'role dev has 1 open tasks')
      return answer()
    })
    const { posted, from, bridge } = await editing({ edit })
    from({ type: 'edit', op: { op: 'set', role: 'dev', field: 'model', value: 'x' } })
    await vi.waitFor(() => expect(posted).toHaveLength(3))
    from({ type: 'edit', op: { op: 'rename_role', role: 'dev', to: 'api' } })
    await vi.waitFor(() => expect(posted).toHaveLength(5))
    expect(posted[3]!.msg).toEqual({
      bridge: OFFICE_BRIDGE,
      type: 'refused',
      index: 1,
      op: { op: 'rename_role', role: 'dev', to: 'api' },
      message: 'role dev has 1 open tasks',
    })
    expect(posted[4]!.msg).toMatchObject({ type: 'team', draft: { ops: 1 } })
    expect(bridge.draft.ops).toBe(1)
    refuse = false
  })

  it('takes the last change back, throws the draft away, and leaves editing only without changes', async () => {
    const { edit, from, bridge } = await editing()
    from({ type: 'edit', op: { op: 'add_role', role: 'a' } })
    from({ type: 'edit', op: { op: 'add_role', role: 'b' } })
    await vi.waitFor(() => expect(bridge.draft.ops).toBe(2))
    from({ type: 'undo' })
    await vi.waitFor(() => expect(edit).toHaveBeenLastCalledWith('fitness/squadra.md', { etag: 'e1', ops: [{ op: 'add_role', role: 'a' }], save: false }))
    from({ type: 'editing', on: false })
    await settle()
    expect(bridge.editing).toBe(true)
    from({ type: 'discard' })
    await vi.waitFor(() => expect(bridge.draft.ops).toBe(0))
    from({ type: 'editing', on: false })
    await settle()
    expect(bridge.editing).toBe(false)
  })

  it('saves only from the window: a page that asks is not heard', async () => {
    const { edit, posted, from, bridge } = await editing()
    from({ type: 'edit', op: { op: 'add_role', role: 'a' } })
    await vi.waitFor(() => expect(posted).toHaveLength(3))
    from({ type: 'save' })
    await settle()
    expect(edit).not.toHaveBeenCalledWith('fitness/squadra.md', expect.objectContaining({ save: true }))
    expect(await bridge.save()).toBe(true)
    expect(edit).toHaveBeenCalledWith('fitness/squadra.md', { etag: 'e1', ops: [{ op: 'add_role', role: 'a' }], save: true })
    expect(posted.some((p) => p.msg.type === 'saved' && p.msg.ok === true)).toBe(true)
    expect(bridge.draft).toMatchObject({ ops: 0, saving: false, error: '' })
    // The next check is on the version the save wrote.
    await vi.waitFor(() => expect(edit).toHaveBeenLastCalledWith('fitness/squadra.md', { etag: 'e2', ops: [], save: false }))
    expect(await bridge.save()).toBe(false)
  })

  it('says when the note changed under the draft, and keeps saying it', async () => {
    // The third check comes after the note changed.
    let checks = 0
    const edit = vi.fn(async (_p: string, body: { save: boolean }) => {
      if (body.save) throw Object.assign(new Error('changed'), { response: { status: 412 } })
      checks++
      return answer({ changed: checks === 3, etag: checks >= 3 ? 'e3' : 'e1' })
    })
    const { posted, from, bridge } = await editing({ edit })
    from({ type: 'edit', op: { op: 'add_role', role: 'a' } })
    await vi.waitFor(() => expect(posted).toHaveLength(3))
    expect(await bridge.save()).toBe(false)
    expect(bridge.draft.error).toBe('conflict')
    expect(posted.find((p) => p.msg.type === 'saved')!.msg).toEqual({ bridge: OFFICE_BRIDGE, type: 'saved', ok: false, error: 'conflict' })
    await vi.waitFor(() => expect(posted.at(-1)!.msg).toMatchObject({ type: 'team', draft: { rebased: true } }))
    from({ type: 'refresh' })
    await vi.waitFor(() => expect(edit).toHaveBeenLastCalledWith('fitness/squadra.md', { etag: 'e3', ops: [{ op: 'add_role', role: 'a' }], save: false }))
    await vi.waitFor(() => expect(posted.at(-1)!.msg).toMatchObject({ draft: { rebased: true } }))
  })

  it('edits nothing where the reader may not write', async () => {
    const edit = vi.fn(async () => answer())
    const { posted, from, bridge } = setup({ read: vi.fn(async () => ({ ...TEAM, etag: 'e1', editable: false })), edit })
    from({ type: 'ready' })
    await vi.waitFor(() => expect(posted).toHaveLength(1))
    from({ type: 'editing', on: true })
    from({ type: 'edit', op: { op: 'add_role', role: 'a' } })
    await settle()
    expect(bridge.editing).toBe(false)
    expect(edit).not.toHaveBeenCalled()
  })
})

describe('openable', () => {
  it('is a markdown note of the project, by a plain path', () => {
    expect(openable('fitness/agents/capo.md', 'fitness')).toBe(true)
    expect(openable('fitness/squadra.MD', 'fitness')).toBe(true)
    expect(openable('fitnessx/a.md', 'fitness')).toBe(false)
    expect(openable('fitness//a.md', 'fitness')).toBe(false)
    expect(openable('fitness/./a.md', 'fitness')).toBe(false)
    expect(openable('/fitness/a.md', 'fitness')).toBe(false)
    expect(openable('fitness/a.png', 'fitness')).toBe(false)
  })
})
