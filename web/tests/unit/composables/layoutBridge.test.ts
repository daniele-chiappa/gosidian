import { describe, expect, it, vi } from 'vitest'
import {
  createLayoutBridge,
  editorPieces,
  LAYOUT_BRIDGE,
  newPage,
  noteField,
  notePdfs,
  withTheRest,
  noteTypeOf,
  type LayoutBridgeDeps,
} from '@/composables/layoutBridge'
import type { Piece } from '@/api/layouts'

// The window's side of the bridge layout/2 (IMP-226): the editor in the
// frame asks, the window opens and saves the note with the API.

const ORIGIN = 'https://layout.example.com'

function setup(over: Partial<LayoutBridgeDeps> = {}) {
  const posted: { msg: Record<string, unknown>; origin: string; transfer: unknown[] }[] = []
  const frame = {
    postMessage: (msg: unknown, origin: string, transfer: Transferable[] = []) => {
      posted.push({ msg: msg as Record<string, unknown>, origin, transfer })
    },
  }
  const states: Record<string, unknown>[] = []
  const deps: LayoutBridgeDeps = {
    path: 'rivista/impaginati/numero 1.md',
    origin: ORIGIN,
    frame: () => frame,
    open: vi.fn(async () => ({
      bytes: new Uint8Array([80, 75, 3, 4]).buffer,
      etag: '"e1"',
      missing: [] as string[],
    })),
    save: vi.fn(async () => ({ etag: '"e2"' })),
    exportFile: vi.fn(async () => ({ path: 'rivista/attachments/abc.pdf', label: 'draft', etag: '"e9"' })),
    pick: vi.fn(async () => ({
      name: 'rivista/attachments/foto.png',
      bytes: new Uint8Array([1, 2]).buffer,
    })),
    pieces: vi.fn(async () => ({ etag: '"n1"', pieces: [] })),
    number: vi.fn(async () => ({ etag: '"n2"', pieces: [] })),
    add: vi.fn(async () => ({ etag: '"n3"', pieces: [], added: undefined })),
    attach: vi.fn(async () => 'rivista/attachments/nuova.png'),
    edit: vi.fn(async () => ({ etag: '"n4"', pieces: [] })),
    reorder: vi.fn(async () => ({ etag: '"n5"', pieces: [] })),
    showPiece: vi.fn(),
    readImage: vi.fn(async () => new Uint8Array([7]).buffer),
    fonts: vi.fn(async () => [] as ArrayBuffer[]),
    onState: (s) => states.push(s),
    ...over,
  }
  const bridge = createLayoutBridge(deps)
  const from = (data: Record<string, unknown>, origin = ORIGIN, source: unknown = frame) =>
    bridge.handle({ data: { bridge: LAYOUT_BRIDGE, ...data }, origin, source })
  return { bridge, deps, posted, states, frame, from }
}

const piece = (over: Partial<Piece>): Piece => ({
  n: 1,
  kind: 'paragraph',
  text: 'Uno.',
  hash: 'h1',
  line: 5,
  blocks: 1,
  ...over,
})

describe('editorPieces', () => {
  it('leaves out what stays out of the design', () => {
    expect(editorPieces([piece({ n: 1 }), piece({ n: 2, excluded: true })]).map((p) => p.id)).toEqual(['1'])
  })

  it('gives an image description a place of its own', () => {
    const got = editorPieces([
      piece({ n: 3, kind: 'image', hash: 'i', image: { src: 'a.png', alt: 'Una foto', alt_hash: 'd' } }),
    ])
    expect(got.map((p) => [p.id, p.kind, p.text, p.hash])).toEqual([
      ['3', 'image', 'Uno.', 'i'],
      ['3.1', 'description', 'Una foto', 'd'],
    ])
  })

  it('has no description for an image without one, or without a number', () => {
    expect(editorPieces([piece({ n: 3, kind: 'image', image: { src: 'a.png' } })])).toHaveLength(1)
    expect(editorPieces([piece({ n: 0, kind: 'image', image: { src: 'a.png', alt: 'x' } })])).toHaveLength(1)
  })
})

describe('layoutBridge, the fonts of the installation', () => {
  it('sends them before the document, each transferred', async () => {
    const [one, two] = [new Uint8Array([1]).buffer, new Uint8Array([2]).buffer]
    const { from, posted } = setup({ fonts: vi.fn(async () => [one, two]) })
    await from({ type: 'ready', engine: 'designcraft 0.17.0' })
    expect(posted.map((p) => p.msg.type).slice(0, 3)).toEqual(['font', 'font', 'open'])
    expect(posted[0]?.msg.bytes).toBe(one)
    expect(posted[0]?.transfer).toEqual([one])
    expect(posted[0]?.origin).toBe(ORIGIN)
  })

  it('opens the document in its fallback fonts when they do not come', async () => {
    const { from, posted } = setup({ fonts: vi.fn(async () => Promise.reject(new Error('down'))) })
    await from({ type: 'ready' })
    expect(posted.map((p) => p.msg.type)).toContain('open')
    expect(posted.map((p) => p.msg.type)).not.toContain('font')
  })
})

describe('layoutBridge, a new design (IMP-232)', () => {
  it('starts on the page the note names, a size in millimetres sent in points', () => {
    expect(newPage()).toEqual({ preset: 'A4' })
    expect(newPage(' A5 ')).toEqual({ preset: 'A5' })
    const size = newPage('148x210mm') as { width: number; height: number }
    expect([size.width, size.height].map((v) => Math.round(v * 100) / 100)).toEqual([419.53, 595.28])
    expect(newPage('100,5 × 200 MM')).toEqual({ width: (100.5 * 72) / 25.4, height: (200 * 72) / 25.4 })
  })

  it('opens the page layout it starts from as a template', async () => {
    const bytes = new ArrayBuffer(4)
    const open = vi.fn(async (path: string) =>
      path === 'rivista/modello.md'
        ? { etag: '"m"', bytes, missing: [] }
        : { etag: '"x"', bytes: null, missing: [], template: 'rivista/modello.md', page: 'A5' },
    )
    const { from, posted } = setup({ open })
    await from({ type: 'ready' })
    const sent = posted.find((p) => p.msg.type === 'open' || p.msg.type === 'new')!
    expect(sent.msg).toEqual(expect.objectContaining({ type: 'open', template: true, bytes }))
    expect(sent.transfer).toEqual([bytes])
  })

  it('says a page layout to start from that does not read, and starts on the page', async () => {
    const open = vi.fn(async (path: string) => {
      if (path === 'rivista/modello.md') throw new Error('gone')
      return { etag: '"x"', bytes: null, missing: [], template: 'rivista/modello.md', page: '170x240mm' }
    })
    const { from, posted, states } = setup({ open })
    await from({ type: 'ready' })
    expect(states).toContainEqual(expect.objectContaining({ error: 'template_unreadable' }))
    expect(posted.find((p) => p.msg.type === 'new')!.msg).toEqual(expect.objectContaining({ width: (170 * 72) / 25.4 }))
  })
})

describe('layoutBridge, the pieces of the note', () => {
  const pieces = vi.fn(async () => ({
    etag: '"n1"',
    pieces: [
      piece({ n: 1 }),
      piece({ n: 2, kind: 'image', image: { src: 'a.png', path: 'r/attachments/a.png' } }),
      piece({ n: 3, kind: 'image', image: { src: 'https://example.com/a.png' } }),
    ],
  }))

  it('sends them after the document', async () => {
    const { from, posted } = setup({ pieces })
    await from({ type: 'ready' })
    expect(posted.map((p) => p.msg.type)).toEqual(['open', 'pieces'])
    const list = JSON.parse(String(posted[1]!.msg.list)) as { id: string }[]
    expect(list.map((p) => p.id)).toEqual(['1', '2', '3'])
  })

  it('sends them again when the note changes', async () => {
    const { from, bridge, posted } = setup({ pieces })
    await from({ type: 'ready' })
    await bridge.refreshPieces()
    expect(posted.filter((p) => p.msg.type === 'pieces')).toHaveLength(2)
  })

  it('does not stop the editor when they do not come', async () => {
    const { from, posted } = setup({ pieces: vi.fn(async () => Promise.reject(new Error('no'))) })
    await from({ type: 'ready' })
    expect(posted.map((p) => p.msg.type)).toEqual(['open'])
  })

  it('answers for an image with its file, transferred', async () => {
    const { from, posted, deps } = setup({ pieces })
    await from({ type: 'ready' })
    await from({ type: 'pick', id: 7, purpose: 'piece:place:2:h1' })
    expect(deps.readImage).toHaveBeenCalledWith('r/attachments/a.png')
    expect(deps.pick).not.toHaveBeenCalled()
    const m = posted.at(-1)!
    expect(m.msg).toMatchObject({ type: 'file', id: 7, name: 'a.png' })
    expect(m.transfer).toEqual([m.msg.bytes])
  })

  it('says no for an image that is no attachment, and for one that fails', async () => {
    const { from, posted } = setup({ pieces })
    await from({ type: 'ready' })
    await from({ type: 'pick', id: 8, purpose: 'piece:place:3:h1' })
    expect(posted.at(-1)!.msg).toMatchObject({ type: 'file', id: 8, cancelled: true })
    await from({ type: 'pick', id: 9, purpose: 'piece:place:44:h1' })
    expect(posted.at(-1)!.msg).toMatchObject({ type: 'file', id: 9, cancelled: true })
  })

  it('tells who waits for a save whether it was kept', async () => {
    const { bridge, from, posted } = setup()
    const kept = bridge.saveNow()
    expect(posted.at(-1)?.msg).toMatchObject({ type: 'save' })
    await from({ type: 'save', name: 'n.designcraft', bytes: new ArrayBuffer(2) })
    expect(await kept).toBe(true)
  })

  it('tells them when it was refused', async () => {
    const { bridge, from } = setup({ save: vi.fn(async () => Promise.reject(new Error('no'))) })
    const kept = bridge.saveNow()
    await from({ type: 'save', name: 'n.designcraft', bytes: new ArrayBuffer(2) })
    expect(await kept).toBe(false)
  })

  it('tells them when the frame goes before saving', async () => {
    const { bridge } = setup()
    const kept = bridge.saveNow()
    bridge.forget()
    expect(await kept).toBe(false)
  })

  it('opens a piece to edit, by its line', async () => {
    const two = piece({ n: 2, line: 12 })
    const { from, deps } = setup({ pieces: vi.fn(async () => ({ etag: '"n1"', pieces: [piece({ n: 1, line: 4 }), two] })) })
    await from({ type: 'ready' })
    await from({ type: 'open_piece', n: '2', line: 12 })
    expect(deps.showPiece).toHaveBeenCalledWith(two)
    // The description of an image opens the image.
    await from({ type: 'open_piece', n: '2.1', line: 12 })
    expect(deps.showPiece).toHaveBeenCalledTimes(2)
    await from({ type: 'open_piece', n: '9', line: 99 })
    expect(deps.showPiece).toHaveBeenCalledTimes(2)
  })

  it('writes a piece, then has its frames catch up with the list', async () => {
    const edit = vi.fn(async () => ({ etag: '"n4"', pieces: [piece({ n: 2, text: 'new' })] }))
    const { bridge, posted, deps } = setup({ edit })
    await bridge.writePiece(piece({ n: 2, hash: 'h2' }), 'new')
    expect(edit).toHaveBeenCalledWith(deps.path, 2, 'h2', 'new')
    expect(posted.map((p) => p.msg.type).slice(-2)).toEqual(['pieces', 'update_piece'])
    expect(posted.at(-1)?.msg.n).toBe('2')
  })

  it('leaves a refused piece to who wrote it', async () => {
    const { bridge, posted } = setup({ edit: vi.fn(async () => Promise.reject(new Error('409'))) })
    await expect(bridge.writePiece(piece({ n: 2 }), 'new')).rejects.toThrow('409')
    expect(posted.some((p) => p.msg.type === 'update_piece')).toBe(false)
  })

  it('reads a piece as it is now', async () => {
    const now = piece({ n: 2, text: 'someone else' })
    const { bridge } = setup({ pieces: vi.fn(async () => ({ etag: '"n1"', pieces: [now] })) })
    expect(await bridge.pieceNow(2)).toEqual(now)
    expect(await bridge.pieceNow(3)).toBeNull()
  })

  it('numbers a piece and sends the list as it is now', async () => {
    const number = vi.fn(async () => ({ etag: '"n2"', pieces: [piece({ n: 1 }), piece({ n: 9 })] }))
    const { from, posted, deps } = setup({ pieces, number })
    await from({ type: 'number_piece', n: '1' })
    expect(number).toHaveBeenCalledWith(deps.path, 1)
    const list = JSON.parse(String(posted.at(-1)!.msg.list)) as { id: string }[]
    expect(list.map((p) => p.id)).toEqual(['1', '9'])
  })

  it('adds a text to the note and sends the list as it is now', async () => {
    const add = vi.fn(async () => ({ etag: '"n3"', pieces: [piece({ n: 1 }), piece({ n: 2 })] }))
    const { from, posted, deps } = setup({ add })
    await from({ type: 'add_piece', text: 'Nuovo', after: 1 })
    expect(add).toHaveBeenCalledWith(deps.path, { after: 1, text: 'Nuovo' })
    expect(posted.at(-1)!.msg.type).toBe('pieces')
  })

  it('tells a frame of the design the number it took in the note', async () => {
    const added = piece({ n: 7, hash: 'h7' })
    const add = vi.fn(async () => ({ etag: '"n3"', pieces: [added], added }))
    const { from, posted } = setup({ add })
    await from({ type: 'add_piece', text: 'Una label', frame: 41 })
    expect(posted.map((p) => p.msg)).toContainEqual(
      expect.objectContaining({ type: 'piece_added', frame: 41, n: '7', hash: 'h7' }),
    )
  })

  it('says a piece added to be placed after the list that holds it (IMP-232)', async () => {
    const added = piece({ n: 7, hash: 'h7' })
    const add = vi.fn(async () => ({ etag: '"n3"', pieces: [added], added }))
    const { from, posted } = setup({ add })
    await from({ type: 'add_piece', text: '## Titolo', after: 2, place: true })
    expect(posted.map((p) => p.msg.type).slice(-2)).toEqual(['pieces', 'piece_added'])
    expect(posted.at(-1)!.msg).toEqual(expect.objectContaining({ n: '7', hash: 'h7' }))
    expect(posted.at(-1)!.msg).not.toHaveProperty('frame')
    await from({ type: 'add_image', after: 2, place: true })
    expect(posted.map((p) => p.msg.type).slice(-2)).toEqual(['pieces', 'piece_added'])
    await from({ type: 'add_piece', text: 'Senza', after: 2 })
    expect(posted.at(-1)!.msg.type).toBe('pieces')
  })

  it('adds the image a frame links, or one chosen', async () => {
    const add = vi.fn(async () => ({ etag: '"n3"', pieces: [] }))
    const { from, deps } = setup({ add })
    await from({ type: 'add_image', link: 'abc.png', frame: 9, after: 2 })
    expect(add).toHaveBeenLastCalledWith(deps.path, { after: 2, image: 'rivista/attachments/abc.png' })
    await from({ type: 'add_image' })
    expect(add).toHaveBeenLastCalledWith(deps.path, { after: 0, image: 'rivista/attachments/foto.png' })
  })

  it('keeps the picture of a frame that has no attachment yet, and adds it', async () => {
    const add = vi.fn(async () => ({ etag: '"n3"', pieces: [] }))
    const attach = vi.fn(async (file: File) => `rivista/attachments/${file.name}`)
    const { from, deps } = setup({ add, attach })
    await from({ type: 'add_image', frame: 9, name: 'foto.png', mime: 'image/png', base64: 'AQID' })
    const file = attach.mock.calls[0]?.[0] as File
    expect([file.name, file.type]).toEqual(['foto.png', 'image/png'])
    expect([...new Uint8Array(await file.arrayBuffer())]).toEqual([1, 2, 3])
    expect(add).toHaveBeenLastCalledWith(deps.path, { after: 0, image: 'rivista/attachments/foto.png' })
  })

  it('says why a picture was not kept, and adds nothing', async () => {
    const add = vi.fn(async () => ({ etag: '"n3"', pieces: [] }))
    const attach = vi.fn(async () => {
      throw new Error('too large')
    })
    const { from, states } = setup({ add, attach })
    await from({ type: 'add_image', frame: 9, name: 'foto.png', mime: 'image/png', base64: 'AQID' })
    expect(add).not.toHaveBeenCalled()
    expect(states.at(-1)).toHaveProperty('error')
  })

  it('adds nothing when no image is chosen', async () => {
    const add = vi.fn(async () => ({ etag: '"n3"', pieces: [] }))
    const { from } = setup({ add, pick: vi.fn(async () => null) })
    await from({ type: 'add_image' })
    expect(add).not.toHaveBeenCalled()
  })

  it('keeps the etag of the design when the text is numbered', async () => {
    const { from, deps } = setup({ pieces })
    await from({ type: 'ready' })
    await from({ type: 'number_piece', n: '1' })
    const bytes = new Uint8Array([9]).buffer
    await from({ type: 'save', name: 'numero 1.designcraft', bytes })
    expect(deps.save).toHaveBeenLastCalledWith(deps.path, bytes, '"e1"')
  })
})

describe('layoutBridge, the words a frame changed in the design (IMP-230)', () => {
  const two = piece({ n: 2, hash: 'h2', text: 'Old.' })
  const listed = () => vi.fn(async () => ({ etag: '"n1"', pieces: [two] }))

  it('writes the words of a frame that read exactly, and the frame keeps them', async () => {
    const edit = vi.fn(async () => ({ etag: '"n4"', pieces: [piece({ n: 2, hash: 'h3', text: 'New.' })] }))
    const { from, posted, deps } = setup({ pieces: listed(), edit })
    await from({ type: 'ready' })
    await from({ type: 'bring_piece', n: '2', hash: 'h2', frame: 41, text: 'New.', exact: true })
    expect(edit).toHaveBeenCalledWith(deps.path, 2, 'h2', 'New.')
    expect(posted.map((p) => p.msg)).toContainEqual(
      expect.objectContaining({ type: 'piece_added', frame: 41, n: '2', hash: 'h3' }),
    )
    expect(posted.some((p) => p.msg.type === 'update_piece')).toBe(false)
    expect(deps.showPiece).not.toHaveBeenCalled()
  })

  it('shows words that do not read exactly before writing them', async () => {
    const { from, deps } = setup({ pieces: listed() })
    await from({ type: 'ready' })
    await from({ type: 'bring_piece', n: '2', hash: 'h2', frame: 41, text: 'New.', exact: false })
    expect(deps.showPiece).toHaveBeenCalledWith(two, { frame: 41, proposed: 'New.' })
    expect(deps.edit).not.toHaveBeenCalled()
  })

  it('shows a piece changed on both sides with the words of the design beside it', async () => {
    const { from, deps } = setup({ pieces: listed() })
    await from({ type: 'ready' })
    await from({ type: 'bring_piece', n: '2', hash: 'h1', frame: 41, design: 'Dal design.', conflict: true })
    expect(deps.showPiece).toHaveBeenCalledWith(two, { frame: 41, design: 'Dal design.' })
    expect(deps.edit).not.toHaveBeenCalled()
  })

  it('shows the piece as it is now when it changed while the words came', async () => {
    const now = piece({ n: 2, hash: 'h9', text: 'Someone else.' })
    const pieces = vi.fn().mockResolvedValueOnce({ etag: '"n1"', pieces: [two] }).mockResolvedValue({ etag: '"n2"', pieces: [now] })
    const edit = vi.fn(async () => Promise.reject({ response: { status: 409 } }))
    const { from, deps } = setup({ pieces, edit })
    await from({ type: 'ready' })
    await from({ type: 'bring_piece', n: '2', hash: 'h2', frame: 41, text: 'New.', exact: true })
    expect(deps.showPiece).toHaveBeenCalledWith(now, { frame: 41, design: 'New.' })
  })

  it('writes what the modal says: the frame keeps its words, and takes the text when it differs', async () => {
    const edit = vi.fn(async () => ({ etag: '"n4"', pieces: [piece({ n: 2, hash: 'h3' })] }))
    const { bridge, posted } = setup({ edit })
    await bridge.writeBrought(two, 'New.', { frame: 41, proposed: 'New.' })
    expect(posted.map((p) => p.msg.type).slice(-2)).toEqual(['pieces', 'piece_added'])
    await bridge.writeBrought(two, 'Changed in the modal.', { frame: 41, proposed: 'New.' })
    expect(posted.map((p) => p.msg.type).slice(-2)).toEqual(['pieces', 'put_back'])
    await bridge.writeBrought(two, 'Chosen.', { frame: 41, design: 'Dal design.' })
    expect(posted.map((p) => p.msg.type).slice(-2)).toEqual(['pieces', 'put_back'])
    expect(posted.at(-1)?.msg.n).toBe('2')
  })

  it("keeps the note's text: the frames of the piece take it again", () => {
    const { bridge, posted } = setup()
    bridge.putBack(two)
    expect(posted.at(-1)?.msg).toEqual({ bridge: LAYOUT_BRIDGE, type: 'put_back', n: '2' })
  })

  it('hears which pieces the design changed, and asks it to bring them all', async () => {
    const { from, bridge, states, posted } = setup()
    await from({ type: 'edited', pieces: '[["2","Inizio"],["10","Altro"]]' })
    expect(states).toContainEqual({ edited: [{ n: '2', preview: 'Inizio' }, { n: '10', preview: 'Altro' }] })
    await from({ type: 'edited', pieces: 'not json' })
    expect(states.at(-1)).toEqual({ edited: [] })
    bridge.bringAll()
    expect(posted.at(-1)?.msg.type).toBe('bring_all')
  })

  it('adds a frame of the design before every piece', async () => {
    const { from, deps } = setup()
    await from({ type: 'add_piece', text: 'Prima', after: -1, frame: 3 })
    expect(deps.add).toHaveBeenCalledWith(deps.path, { after: -1, text: 'Prima' })
  })
})

describe('layoutBridge', () => {
  it('opens the note when the editor is ready, its package transferred', async () => {
    const { from, posted, deps, states } = setup()
    await from({ type: 'ready', engine: 'designcraft 0.17.0' })
    expect(deps.open).toHaveBeenCalledWith('rivista/impaginati/numero 1.md')
    const { msg, origin, transfer } = posted[0]!
    expect(origin).toBe(ORIGIN)
    expect(msg).toMatchObject({ bridge: LAYOUT_BRIDGE, type: 'open', name: 'numero 1.designcraft' })
    expect(transfer).toEqual([msg.bytes])
    expect(states).toContainEqual(
      expect.objectContaining({ ready: true, engine: 'designcraft 0.17.0' }),
    )
  })

  it('starts a new document for a note without a layout', async () => {
    const { from, posted } = setup({
      open: vi.fn(async () => ({ bytes: null, etag: '"e0"', missing: [] })),
    })
    await from({ type: 'ready' })
    expect(posted[0]!.msg).toMatchObject({ type: 'new', name: 'numero 1', preset: 'A4' })
  })

  it('saves with the etag it read, then with the new one', async () => {
    const { from, deps, posted, states } = setup()
    await from({ type: 'ready' })
    const bytes = new Uint8Array([9]).buffer
    await from({ type: 'save', name: 'numero 1.designcraft', bytes })
    expect(deps.save).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', bytes, '"e1"')
    await from({ type: 'save', name: 'numero 1.designcraft', bytes })
    expect(deps.save).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', bytes, '"e2"')
    expect(posted.filter((p) => p.msg.type === 'saved').map((p) => p.msg.ok)).toEqual([true, true])
    expect(states).toContainEqual(expect.objectContaining({ saving: false, error: '' }))
  })

  it('leaves to the editor whether the document is still unsaved', async () => {
    const { from, states } = setup()
    await from({ type: 'ready' })
    await from({ type: 'save', name: 'x.designcraft', bytes: new ArrayBuffer(1) })
    expect(states.some((s) => 'dirty' in s)).toBe(false)
  })

  it('saves one at a time, each with the etag of the one before', async () => {
    let finish: (v: { etag: string }) => void = () => {}
    const save = vi
      .fn<LayoutBridgeDeps['save']>()
      .mockImplementationOnce(() => new Promise((r) => (finish = r)))
      .mockResolvedValueOnce({ etag: '"e3"' })
    const { from, posted } = setup({ save })
    await from({ type: 'ready' })
    const first = from({ type: 'save', name: 'x.designcraft', bytes: new ArrayBuffer(1) })
    const second = from({ type: 'save', name: 'x.designcraft', bytes: new ArrayBuffer(2) })
    await vi.waitFor(() => expect(save).toHaveBeenCalledTimes(1))
    finish({ etag: '"e2"' })
    await Promise.all([first, second])
    expect(save.mock.calls.map((c) => c[2])).toEqual(['"e1"', '"e2"'])
    expect(posted.filter((p) => p.msg.type === 'saved')).toHaveLength(2)
  })

  it('overwrites a note changed meanwhile only when asked, with the document as it is', async () => {
    const conflict = Object.assign(new Error('note was modified'), { response: { status: 412 } })
    const save = vi
      .fn<LayoutBridgeDeps['save']>()
      .mockRejectedValueOnce(conflict)
      .mockResolvedValueOnce({ etag: '"e3"' })
      .mockResolvedValueOnce({ etag: '"e4"' })
    const { bridge, from, posted, states } = setup({ save })
    await from({ type: 'ready' })
    await from({ type: 'save', name: 'x.designcraft', bytes: new ArrayBuffer(1) })
    expect(states.at(-1)).toMatchObject({ error: 'conflict' })

    bridge.overwrite()
    // The editor is asked for the document: it may have changed since the save refused.
    expect(posted.at(-1)?.msg).toMatchObject({ type: 'save' })
    expect(posted.at(-1)?.msg.bytes).toBeUndefined()
    const now = new ArrayBuffer(2)
    await from({ type: 'save', name: 'x.designcraft', bytes: now })
    expect(save).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', now, '')
    expect(posted.at(-1)?.msg).toMatchObject({ type: 'saved', ok: true })

    // That was for one save: the next checks the etag again.
    await from({ type: 'save', name: 'x.designcraft', bytes: now })
    expect(save).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', now, '"e3"')
  })

  it('says a save that failed, the note changed meanwhile among them', async () => {
    const conflict = Object.assign(new Error('note was modified'), { response: { status: 412 } })
    const { from, posted, states } = setup({ save: vi.fn(async () => Promise.reject(conflict)) })
    await from({ type: 'ready' })
    await from({ type: 'save', name: 'x.designcraft', bytes: new ArrayBuffer(1) })
    expect(posted.at(-1)?.msg).toMatchObject({ type: 'saved', ok: false })
    expect(states).toContainEqual(expect.objectContaining({ saving: false, error: 'conflict' }))
  })

  it('asks the editor to save and to run the commands of the bar', () => {
    const { bridge, posted } = setup()
    bridge.requestSave()
    expect(posted.at(-1)?.msg).toEqual({ bridge: LAYOUT_BRIDGE, type: 'save' })
    bridge.run('app.exportPng')
    expect(posted.at(-1)?.msg).toEqual({ bridge: LAYOUT_BRIDGE, type: 'run', id: 'app.exportPng' })
    // The PDF of this one comes back, to keep beside the note (IMP-229).
    bridge.run('app.exportPdf', { toHost: true })
    expect(posted.at(-1)?.msg).toEqual({ bridge: LAYOUT_BRIDGE, type: 'run', id: 'app.exportPdf', to_host: true })
  })

  it('checks the etag of a save asked from the bar', async () => {
    const { bridge, from, deps } = setup()
    await from({ type: 'ready' })
    bridge.requestSave()
    const bytes = new ArrayBuffer(1)
    await from({ type: 'save', name: 'n.designcraft', bytes })
    expect(deps.save).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', bytes, '"e1"')
  })

  it('forgets what it showed of the frame that goes', async () => {
    const { bridge, from, states } = setup()
    await from({ type: 'ready' })
    await from({ type: 'save', name: 'x.designcraft', bytes: new ArrayBuffer(1) })
    bridge.forget()
    expect(states.at(-1)).toMatchObject({ error: '', dirty: false, savedAt: 0, exported: '', exportedLabel: '' })
  })

  it('keeps an exported PDF beside the note', async () => {
    const { from, deps, states } = setup()
    const bytes = new ArrayBuffer(3)
    await from({ type: 'export', name: 'numero 1.pdf', mime: 'application/pdf', bytes })
    expect(deps.exportFile).toHaveBeenCalledWith(
      'rivista/impaginati/numero 1.md',
      'numero 1.pdf',
      bytes,
    )
    expect(states).toContainEqual(
      expect.objectContaining({ exported: 'rivista/attachments/abc.pdf', exportedLabel: 'draft' }),
    )
  })

  it('saves after an export with the etag of the design: an export does not change it', async () => {
    const { from, deps, posted } = setup()
    await from({ type: 'ready' })
    await from({ type: 'export', name: 'n.pdf', mime: 'application/pdf', bytes: new ArrayBuffer(3) })
    const bytes = new ArrayBuffer(1)
    await from({ type: 'save', name: 'n.designcraft', bytes })
    expect(deps.save).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', bytes, '"e1"')
    expect(posted.at(-1)?.msg).toMatchObject({ type: 'saved', ok: true })
  })

  it('takes a save and an export in turn', async () => {
    let finish: (v: { path: string; label: string; etag: string }) => void = () => {}
    const exportFile = vi
      .fn<LayoutBridgeDeps['exportFile']>()
      .mockImplementationOnce(() => new Promise((r) => (finish = r)))
    const { from, deps } = setup({ exportFile })
    await from({ type: 'ready' })
    const exporting = from({ type: 'export', name: 'n.pdf', bytes: new ArrayBuffer(3) })
    const saving = from({ type: 'save', name: 'n.designcraft', bytes: new ArrayBuffer(1) })
    await vi.waitFor(() => expect(exportFile).toHaveBeenCalled())
    expect(deps.save).not.toHaveBeenCalled()
    finish({ path: 'a.pdf', label: 'draft', etag: '"e8"' })
    await Promise.all([exporting, saving])
    expect(vi.mocked(deps.save).mock.lastCall?.[2]).toBe('"e1"')
  })

  it('answers a pick with the image chosen, or cancelled', async () => {
    const { from, posted } = setup()
    await from({ type: 'pick', id: 7, purpose: 'place' })
    expect(posted.at(-1)?.msg).toMatchObject({
      type: 'file',
      id: 7,
      name: 'rivista/attachments/foto.png',
    })
    expect(posted.at(-1)?.transfer).toHaveLength(1)
    const none = setup({ pick: vi.fn(async () => null) })
    await none.from({ type: 'pick', id: 8, purpose: 'place' })
    expect(none.posted.at(-1)?.msg).toMatchObject({ type: 'file', id: 8, cancelled: true })
    // Opening another document in the editor is not part of the bridge yet.
    await none.from({ type: 'pick', id: 9, purpose: 'open' })
    expect(none.deps.pick).toHaveBeenCalledTimes(1)
    expect(none.posted.at(-1)?.msg).toMatchObject({ type: 'file', id: 9, cancelled: true })
  })

  it('passes on whether the document has changes not saved', async () => {
    const { from, states } = setup()
    await from({ type: 'dirty', dirty: true })
    expect(states.at(-1)).toEqual({ dirty: true })
  })

  it('listens only to its frame, from the app origin, under its contract', async () => {
    const { bridge, from, deps, posted, frame } = setup()
    await from({ type: 'ready' }, 'https://evil.example.com')
    await from({ type: 'ready' }, ORIGIN, {})
    await bridge.handle({
      data: { bridge: 'other/1', type: 'ready' },
      origin: ORIGIN,
      source: frame,
    })
    await bridge.handle({ data: 'ready', origin: ORIGIN, source: frame })
    await from({ type: 'save', bytes: 'not bytes' })
    expect(deps.open).not.toHaveBeenCalled()
    expect(deps.save).not.toHaveBeenCalled()
    expect(posted).toHaveLength(0)
  })
})

describe('noteField', () => {
  it('reads a field of the frontmatter, not of the text', () => {
    const note = '---\ntitle: Rivista\nlayout_file: "r/attachments/ab.json"\n---\nlayout_preview: no\n'
    expect(noteField(note, 'layout_file')).toBe('r/attachments/ab.json')
    expect(noteField(note, 'layout_preview')).toBe('')
    expect(noteField('# no frontmatter\nlayout_file: x\n', 'layout_file')).toBe('')
    expect(noteField('---\r\nlayout_file: a.json\r\n---\r\n', 'layout_file')).toBe('a.json')
  })
})

describe('noteTypeOf', () => {
  it('reads the type of a note from its frontmatter', () => {
    expect(noteTypeOf('---\ntitle: Rivista\ntype: layout\n---\n')).toBe('layout')
    expect(noteTypeOf('---\r\ntype: "layout"\r\n---\r\n# x')).toBe('layout')
    expect(noteTypeOf('# no frontmatter\ntype: layout\n')).toBe('')
    expect(noteTypeOf('---\ntitle: x\n---\ntype: layout\n')).toBe('')
  })
})

describe('notePdfs (IMP-229)', () => {
  it('reads the PDFs a note lists, the draft\'s of an older layout_pdf too', () => {
    const note = '---\ntitle: R\nlayout_pdfs: ["V3: r/attachments/a.pdf", "draft: r/attachments/b.pdf", "bad"]\n---\nbody'
    expect(notePdfs(note)).toEqual([
      { label: 'V3', path: 'r/attachments/a.pdf' },
      { label: 'draft', path: 'r/attachments/b.pdf' },
    ])
    expect(notePdfs('---\nlayout_pdf: r/attachments/c.pdf\n---\n')).toEqual([{ label: 'draft', path: 'r/attachments/c.pdf' }])
    expect(notePdfs('---\nlayout_pdfs: [broken\n---\n')).toEqual([])
    expect(notePdfs('no frontmatter')).toEqual([])
  })
})

describe('the order of the pieces (IMP-231)', () => {
  const four = [
    piece({ n: 0, text: 'In cima.', hash: 'h0' }),
    piece({ n: 1, hash: 'h1' }),
    piece({ n: 4, text: 'Quattro.', hash: 'h4' }),
    piece({ n: 4, text: 'Copia.', hash: 'hx', duplicate: true }),
    piece({ n: 2, text: 'Due.', hash: 'h2' }),
  ]
  const ready = async (over: Partial<LayoutBridgeDeps> = {}) => {
    const t = setup({ pieces: vi.fn(async () => ({ etag: '"n1"', pieces: four })), ...over })
    await t.from({ type: 'ready' })
    return t
  }

  it('moves a dragged piece after another, each piece with the hash it was read with', async () => {
    const { from, deps, posted } = await ready()
    vi.mocked(deps.reorder).mockResolvedValueOnce({ etag: '"n5"', pieces: [four[0]!, four[4]!, four[1]!, four[2]!, four[3]!] })
    await from({ type: 'move_piece', n: 2, after: 0 })
    expect(deps.reorder).not.toHaveBeenCalled()
    await from({ type: 'move_piece', n: 2, after: -1 })
    expect(deps.reorder).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', [
      { n: 2, hash: 'h2' },
      { n: 1, hash: 'h1' },
      { n: 4, hash: 'h4' },
    ])
    expect(JSON.parse(String(posted.at(-1)?.msg.list)).map((p: { id: string }) => p.id)).toEqual(['0', '2', '1', '4', '4'])
    await from({ type: 'move_piece', n: 1, after: 4 })
    expect(deps.reorder).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', [
      { n: 2, hash: 'h2' },
      { n: 4, hash: 'h4' },
      { n: 1, hash: 'h1' },
    ])
  })

  it('writes the order of the pages the editor sends', async () => {
    const { from, deps } = await ready()
    await from({ type: 'reorder', order: '[4,2,1]' })
    expect(deps.reorder).toHaveBeenLastCalledWith('rivista/impaginati/numero 1.md', [
      { n: 4, hash: 'h4' },
      { n: 2, hash: 'h2' },
      { n: 1, hash: 'h1' },
    ])
    await from({ type: 'reorder', order: 'not json' })
    expect(deps.reorder).toHaveBeenCalledTimes(1)
  })

  it('a note changed meanwhile: nothing written, the pieces as they are now, and it says so', async () => {
    const changed = Object.assign(new Error('409'), { response: { status: 409 } })
    const { from, deps, states } = await ready({ reorder: vi.fn(async () => Promise.reject(changed)) })
    vi.mocked(deps.pieces).mockClear()
    await from({ type: 'reorder', order: '[2,1,4]' })
    expect(states.at(-1)).toMatchObject({ error: 'order_changed' })
    expect(deps.pieces).toHaveBeenCalled()
    // An order that does not name the pieces the bridge holds.
    vi.mocked(deps.pieces).mockClear()
    await from({ type: 'reorder', order: '[2,1]' })
    expect(states.at(-1)).toMatchObject({ error: 'order_changed' })
    expect(deps.pieces).toHaveBeenCalled()
  })
})

describe('withTheRest (IMP-231)', () => {
  it('puts the pieces the editor does not see after the piece before them', () => {
    expect(withTheRest([3, 1], [1, 2, 3, 4])).toEqual([3, 4, 1, 2])
    expect(withTheRest([2], [5, 2])).toEqual([5, 2])
    expect(withTheRest([1, 2], [1, 2])).toEqual([1, 2])
  })
})
