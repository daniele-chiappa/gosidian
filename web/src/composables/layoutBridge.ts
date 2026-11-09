/**
 * The window's side of the bridge layout/2 (IMP-226, docs/plugins.md#apps):
 * the page-layout editor of a plugin, in the window's frame, asks over
 * postMessage, and the window opens and saves the note with the API and the
 * rights of whoever is logged in. The editor holds no credentials.
 *
 * A message counts only from the window's own frame, at the app's origin,
 * under the contract; the answers go to that origin only. The editor's side
 * is in designcraft's fork (apps/designcraft-web/src/host.rs).
 *
 * The editor decides whether its document is saved: it hears `saved` and
 * tells `dirty`, since what changed during a save stays unsaved. A design
 * saved meanwhile is overwritten only on request, with the document as it is
 * by then, which the editor is asked for. The note's text is another matter:
 * it is written while the design is open, and the editor hears its pieces
 * again each time. Words changed in a frame come to the note when asked
 * (IMP-230): written at once when they read exactly, shown first otherwise.
 */
import { base } from './planciaKey'
import type { NewPiece, NotePieces, OpenedLayout, Piece } from '@/api/layouts'

export const LAYOUT_BRIDGE = 'layout/2'
/** The page of a new document, which File › Document Setup changes. */
const NEW_PAGE = 'A4'
const PT_PER_MM = 72 / 25.4

/** The page a new design starts on (IMP-232): a size in millimetres, as
 *  148x210mm, sent in points, or the name of one of the editor's pages. */
export function newPage(page?: string): { preset: string } | { width: number; height: number } {
  const m = /^(\d+(?:[.,]\d+)?)\s*[x×]\s*(\d+(?:[.,]\d+)?)\s*mm$/i.exec((page ?? '').trim())
  if (m) {
    const pt = (v: string) => Number(v.replace(',', '.')) * PT_PER_MM
    return { width: pt(m[1]!), height: pt(m[2]!) }
  }
  return { preset: page?.trim() || NEW_PAGE }
}

/** What the window shows of the bridge. error is a code: conflict (the
 *  design was saved since it was opened), too_large, or the server's message. */
export interface LayoutBridgeState {
  ready: boolean
  engine: string
  dirty: boolean
  saving: boolean
  savedAt: number
  error: string
  missing: string[]
  /** The PDF kept beside the note last, and whose it is: a version's label,
   *  or the draft's (IMP-229). */
  exported: string
  exportedLabel: string
  /** The pieces whose words changed in the design and not in the note. */
  edited: { n: string; preview: string }[]
}

/** Where a piece shown to edit comes from, when a frame of the design sends
 *  it: the words carried into its text, to write after a look, or the
 *  design's words beside a piece changed on both sides. */
export interface BroughtFrom {
  frame: number
  proposed?: string
  design?: string
}

export interface LayoutBridgeDeps {
  /** The note the window opened. */
  path: string
  /** The app's origin, from GET /api/v1/plugins/apps. */
  origin: string
  /** The frame's window, null before it loads. */
  frame: () => {
    postMessage: (msg: unknown, origin: string, transfer?: Transferable[]) => void
  } | null
  open: (path: string) => Promise<OpenedLayout>
  save: (path: string, bytes: ArrayBuffer, etag: string) => Promise<{ etag: string }>
  exportFile: (
    path: string,
    name: string,
    bytes: ArrayBuffer,
  ) => Promise<{ path: string; label: string; etag: string }>
  /** An image chosen among the project's, null when none was. */
  pick: (purpose: string) => Promise<{ name: string; bytes: ArrayBuffer } | null>
  pieces: (path: string) => Promise<NotePieces>
  /** Numbers the blocks of piece n. */
  number: (path: string, n: number) => Promise<NotePieces>
  /** Adds a piece to the note. */
  add: (path: string, piece: NewPiece) => Promise<NotePieces & { added?: Piece }>
  /** Keeps a file among the project's attachments: its path. */
  attach: (file: File) => Promise<string>
  /** Writes a new text for a piece. */
  edit: (path: string, n: number, hash: string, text: string) => Promise<NotePieces>
  /** The numbered pieces in that order, each with its hash (IMP-231). */
  reorder: (path: string, order: { n: number; hash: string }[]) => Promise<NotePieces>
  /** Opens a piece to edit, with what a frame of the design sends. */
  showPiece: (piece: Piece, from?: BroughtFrom) => void
  /** The bytes of an attachment. */
  readImage: (path: string) => Promise<ArrayBuffer>
  /** The installation's fonts, each a file. */
  fonts: () => Promise<ArrayBuffer[]>
  onState: (s: Partial<LayoutBridgeState>) => void
}

/** order with the pieces it does not name, those kept out of the design,
 *  each right after the piece before it in now. A number not in now, or one
 *  named twice, is left for the caller to refuse. */
export function withTheRest(order: number[], now: number[]): number[] {
  const out = [...order]
  now.forEach((n, i) => {
    if (out.includes(n)) return
    const before = now
      .slice(0, i)
      .reverse()
      .find((m) => out.includes(m))
    out.splice(before === undefined ? 0 : out.indexOf(before) + 1, 0, n)
  })
  return out
}

/** A piece as the editor's panel takes it. */
export interface EditorPiece {
  id: string
  kind: string
  text: string
  hash: string
  line: number
  blocks: number
  duplicate: boolean
  /** What an image's own note says of it, to read and copy. */
  described: string
}

/** What the design can place: not the pieces kept out of it, and an image's
 *  description apart from it, as a photo and its caption are. */
export function editorPieces(pieces: Piece[]): EditorPiece[] {
  const out: EditorPiece[] = []
  for (const p of pieces) {
    if (p.excluded) continue
    const { kind, text, hash, line, blocks } = p
    const duplicate = p.duplicate === true
    const described = p.image?.described ?? ''
    out.push({ id: String(p.n), kind, text, hash, line, blocks, duplicate, described })
    if (p.n > 0 && p.image?.alt)
      out.push({
        id: `${p.n}.1`,
        kind: 'description',
        text: p.image.alt,
        hash: p.image.alt_hash ?? '',
        line,
        blocks: 1,
        duplicate,
        described: '',
      })
  }
  return out
}

export interface BridgeEvent {
  data: unknown
  origin: string
  source: unknown
}

function statusOf(e: unknown): number | undefined {
  return (e as { response?: { status?: number } })?.response?.status
}

function errorCode(e: unknown): string {
  const status = (e as { response?: { status?: number } })?.response?.status
  if (status === 412) return 'conflict'
  if (status === 413) return 'too_large'
  const msg = (e as { response?: { data?: { error?: { message?: string } } } })?.response?.data
    ?.error?.message
  return msg || (e instanceof Error ? e.message : String(e))
}

export function createLayoutBridge(deps: LayoutBridgeDeps) {
  let etag = ''
  // The next save goes without the etag: overwrite() asked for it.
  let overwriting = false
  // One at a time: a save takes the etag of what wrote the note before it,
  // and an export writes it too, with the path of its PDF.
  let turn: Promise<void> = Promise.resolve()
  const inTurn = (job: () => Promise<void>) => (turn = turn.then(job))
  let pieces: Piece[] = []
  // Who waits for the next save to end: they hear whether it was kept.
  let waiting: ((kept: boolean) => void)[] = []
  const settle = (kept: boolean) => {
    for (const hear of waiting) hear(kept)
    waiting = []
  }
  const post = (msg: Record<string, unknown>, transfer: Transferable[] = []) =>
    deps.frame()?.postMessage({ bridge: LAYOUT_BRIDGE, ...msg }, deps.origin, transfer)

  /** Before the document: it finds them, and no font is missing to fix. A
   *  catalog that does not come leaves it in its fallback fonts, open. */
  async function sendFonts() {
    const files = await deps.fonts().catch(() => [] as ArrayBuffer[])
    for (const bytes of files) post({ type: 'font', bytes }, [bytes])
  }

  async function openNote() {
    try {
      const opened = await deps.open(deps.path)
      etag = opened.etag
      deps.onState({ missing: opened.missing })
      if (opened.bytes)
        post({ type: 'open', name: `${base(deps.path)}.designcraft`, bytes: opened.bytes }, [
          opened.bytes,
        ])
      else if (opened.template) await openTemplate(opened.template, opened.page)
      else post({ type: 'new', name: base(deps.path), ...newPage(opened.page) })
    } catch (e) {
      deps.onState({ error: errorCode(e) })
    }
  }

  /** A new design from another page layout: its look, none of its pages
   *  (IMP-232). One that does not read leaves the window saying so, and the
   *  design starts on the page asked. */
  async function openTemplate(template: string, page?: string) {
    const from = await deps.open(template).catch(() => null)
    if (from?.bytes) {
      post({ type: 'open', name: `${base(deps.path)}.designcraft`, bytes: from.bytes, template: true }, [from.bytes])
      return
    }
    deps.onState({ error: 'template_unreadable' })
    post({ type: 'new', name: base(deps.path), ...newPage(page) })
  }

  async function save(bytes: ArrayBuffer) {
    deps.onState({ saving: true, error: '' })
    const tag = overwriting ? '' : etag
    overwriting = false
    try {
      etag = (await deps.save(deps.path, bytes, tag)).etag
      deps.onState({ saving: false, error: '', savedAt: Date.now() })
      post({ type: 'saved', ok: true })
      settle(true)
    } catch (e) {
      const error = errorCode(e)
      deps.onState({ saving: false, error })
      post({ type: 'saved', ok: false, error })
      settle(false)
    }
  }

  async function exportFile(name: string, bytes: ArrayBuffer) {
    try {
      const done = await deps.exportFile(deps.path, name, bytes)
      deps.onState({ exported: done.path, exportedLabel: done.label, error: '' })
    } catch (e) {
      deps.onState({ error: errorCode(e) })
    }
  }

  function sendPieces(list: Piece[]) {
    pieces = list
    post({ type: 'pieces', list: JSON.stringify(editorPieces(list)) })
  }

  /** The note changed: the panel hears what it holds now. A list that does
   *  not come leaves the last one, and the editor at work. */
  async function refreshPieces() {
    const got = await deps.pieces(deps.path).catch(() => null)
    if (got) sendPieces(got.pieces)
  }

  /** The numbered pieces in the order the editor asks, each with the hash
   *  it was read with: a piece changed meanwhile leaves the note as it is,
   *  and the panel hears the pieces as they are now (IMP-231). */
  async function reorder(ns: number[]) {
    const held = new Map(pieces.filter((p) => p.n > 0 && !p.duplicate).map((p) => [p.n, p.hash]))
    ns = withTheRest(ns, [...held.keys()])
    if (ns.length !== held.size || ns.some((n) => !held.has(n))) {
      await refreshPieces()
      deps.onState({ error: 'order_changed' })
      return
    }
    try {
      sendPieces((await deps.reorder(deps.path, ns.map((n) => ({ n, hash: held.get(n) ?? '' })))).pieces)
      deps.onState({ error: '' })
    } catch (e) {
      const changed = (e as { response?: { status?: number } })?.response?.status === 409
      deps.onState({ error: changed ? 'order_changed' : errorCode(e) })
      if (changed) await refreshPieces()
    }
  }

  /** A piece dragged in the panel, or moved with Alt+arrows: after the piece
   *  numbered after, or first of the numbered ones with -1. */
  async function movePiece(n: number, after: number) {
    const ns = pieces.filter((p) => p.n > 0 && !p.duplicate).map((p) => p.n)
    if (!ns.includes(n) || n === after || (after >= 0 && !ns.includes(after))) return
    const rest = ns.filter((m) => m !== n)
    rest.splice(after < 0 ? 0 : rest.indexOf(after) + 1, 0, n)
    await reorder(rest)
  }

  async function numberPiece(n: number) {
    try {
      sendPieces((await deps.number(deps.path, n)).pieces)
    } catch (e) {
      deps.onState({ error: errorCode(e) })
    }
  }

  /** A piece the design adds. A frame that goes with it hears its number;
   *  one added to be placed is said after the list that holds it, for the
   *  editor to place it (IMP-232). */
  async function addPiece(piece: NewPiece, frame: unknown, place = false) {
    try {
      const { pieces: list, added } = await deps.add(deps.path, piece)
      if (typeof frame === 'number' && added)
        post({ type: 'piece_added', frame, n: String(added.n), hash: added.hash })
      sendPieces(list)
      if (place && typeof frame !== 'number' && added)
        post({ type: 'piece_added', n: String(added.n), hash: added.hash })
    } catch (e) {
      deps.onState({ error: errorCode(e) })
    }
  }

  /** The picture of a frame among the attachments: it has none until the
   *  document is opened again. */
  function attachPicture(d: Record<string, unknown>) {
    const bytes = Uint8Array.from(atob(String(d.base64)), (c) => c.charCodeAt(0))
    return deps.attach(new File([bytes], String(d.name), { type: String(d.mime ?? '') }))
  }

  /** An image of the design for the note: the attachment the frame links,
   *  its picture, or one the user chooses. */
  async function addImage(d: Record<string, unknown>) {
    const after = Number(d.after) || 0
    let image = ''
    try {
      if (typeof d.link === 'string' && d.link)
        image = `${deps.path.split('/')[0]}/attachments/${d.link}`
      else if (typeof d.base64 === 'string') image = await attachPicture(d)
      else image = (await deps.pick('place').catch(() => null))?.name ?? ''
    } catch (e) {
      deps.onState({ error: errorCode(e) })
    }
    if (image) await addPiece({ after, image }, d.frame, d.place === true)
  }

  /** The file of an image piece, asked as piece:<what>:<n>:<hash>. */
  async function pieceImage(purpose: string) {
    const n = purpose.split(':')[2]
    const path = pieces.find((p) => String(p.n) === n)?.image?.path
    if (!path) return null
    const bytes = await deps.readImage(path).catch(() => null)
    return bytes && { name: path.split('/').pop() ?? path, bytes }
  }

  async function pick(id: number, purpose: string) {
    // Opening another document inside the editor is not part of the bridge yet.
    const chosen = purpose.startsWith('piece:')
      ? await pieceImage(purpose)
      : purpose === 'place'
        ? await deps.pick(purpose).catch(() => null)
        : null
    if (chosen) post({ type: 'file', id, name: chosen.name, bytes: chosen.bytes }, [chosen.bytes])
    else post({ type: 'file', id, cancelled: true })
  }

  async function handle(e: BridgeEvent) {
    const frame = deps.frame()
    if (!frame || e.source !== frame || e.origin !== deps.origin) return
    const d = e.data as Record<string, unknown> | null
    if (!d || typeof d !== 'object' || d.bridge !== LAYOUT_BRIDGE) return
    switch (d.type) {
      case 'ready':
        deps.onState({ ready: true, engine: typeof d.engine === 'string' ? d.engine : '' })
        await sendFonts()
        await openNote()
        await refreshPieces()
        break
      case 'dirty':
        deps.onState({ dirty: d.dirty === true })
        break
      case 'save':
        if (d.bytes instanceof ArrayBuffer) {
          const bytes = d.bytes
          await inTurn(() => save(bytes))
        }
        break
      case 'export':
        if (d.bytes instanceof ArrayBuffer && typeof d.name === 'string') {
          const [name, bytes] = [d.name, d.bytes]
          await inTurn(() => exportFile(name, bytes))
        }
        break
      case 'pick':
        if (typeof d.id === 'number') await pick(d.id, String(d.purpose ?? ''))
        break
      case 'open_piece': {
        // By its line: the description of an image, and a piece without a
        // number, have the same one as their piece, or none of their own.
        const piece = pieces.find((p) => p.line === d.line)
        if (piece) deps.showPiece(piece)
        break
      }
      case 'number_piece':
        await numberPiece(Number(d.n))
        break
      case 'move_piece':
        await inTurn(() => movePiece(Number(d.n), Number(d.after)))
        break
      case 'reorder': {
        let order: unknown = []
        try {
          order = JSON.parse(String(d.order))
        } catch {
          order = []
        }
        if (Array.isArray(order) && order.length > 0 && order.every((n) => Number.isInteger(n)))
          await inTurn(() => reorder(order as number[]))
        break
      }
      case 'add_piece':
        if (typeof d.text === 'string') {
          const text = d.text
          // In turn: frames brought one after the other keep their order.
          await inTurn(() => addPiece({ after: Number(d.after) || 0, text }, d.frame, d.place === true))
        }
        break
      case 'add_image':
        await inTurn(() => addImage(d))
        break
      case 'bring_piece':
        await inTurn(() => bringPiece(d))
        break
      case 'edited':
        deps.onState({ edited: editedOf(d.pieces) })
        break
    }
  }

  /** The words a frame changed in the design, for piece n: written at once
   *  when they read exactly as the frame does, shown first otherwise, and
   *  beside the piece when it changed in the note too. */
  async function bringPiece(d: Record<string, unknown>) {
    const [n, frame] = [Number(d.n), Number(d.frame)]
    const piece = pieces.find((p) => p.n === n)
    if (!piece || !Number.isFinite(frame)) return
    const text = typeof d.text === 'string' ? d.text : ''
    if (d.conflict === true || piece.hash !== d.hash) {
      deps.showPiece(piece, { frame, design: typeof d.design === 'string' ? d.design : text })
      return
    }
    if (d.exact !== true) {
      deps.showPiece(piece, { frame, proposed: text })
      return
    }
    try {
      await writeBrought(piece, text, { frame, proposed: text })
    } catch (e) {
      if (statusOf(e) !== 409) {
        deps.onState({ error: errorCode(e) })
        return
      }
      const now = await pieceNow(n)
      if (now) deps.showPiece(now, { frame, design: text })
    }
  }

  /** A piece's text written from what a frame sent: the frame keeps its
   *  words when they are what was written, and takes the text otherwise. A
   *  refusal is the caller's to show. */
  async function writeBrought(piece: Piece, text: string, from: BroughtFrom) {
    const list = (await deps.edit(deps.path, piece.n, piece.hash, text)).pieces
    sendPieces(list)
    const now = list.find((p) => p.n === piece.n)
    if (!now) return
    if (from.proposed !== undefined && text === from.proposed)
      post({ type: 'piece_added', frame: from.frame, n: String(piece.n), hash: now.hash })
    else putBack(now)
  }

  /** The note's text stays: the frames of the piece take it again, words
   *  changed in the design or not. */
  function putBack(piece: Piece) {
    post({ type: 'put_back', n: String(piece.n) })
  }

  /** Every piece changed in the design only comes to the note. */
  function bringAll() {
    post({ type: 'bring_all' })
  }

  /** A new text for a piece: the panel hears the list, and the frames that
   *  show the piece catch up. A refusal is the caller's to show. */
  async function writePiece(piece: Piece, text: string) {
    sendPieces((await deps.edit(deps.path, piece.n, piece.hash, text)).pieces)
    post({ type: 'update_piece', n: String(piece.n) })
  }

  /** The piece as it is now, after a change it did not know: null once gone. */
  async function pieceNow(n: number): Promise<Piece | null> {
    await refreshPieces()
    return pieces.find((p) => p.n === n) ?? null
  }

  /** Ask the editor for the document, as Ctrl+S in it. */
  function requestSave() {
    post({ type: 'save' })
  }

  /** The same, for who goes on once the document is kept: false if it was not. */
  function saveNow(): Promise<boolean> {
    return new Promise((resolve) => {
      waiting.push(resolve)
      requestSave()
    })
  }

  /** Run a command of the bar in the editor: it has a fixed list of them.
   *  With toHost, the PDF it writes comes here, to keep beside the note,
   *  instead of the browser's downloads (IMP-229). */
  function run(id: string, opts: { toHost?: boolean } = {}) {
    post(opts.toHost ? { type: 'run', id, to_host: true } : { type: 'run', id })
  }

  /** Save over a note changed meanwhile: the editor sends the document. */
  function overwrite() {
    overwriting = true
    requestSave()
  }

  /** A new frame comes up: what it opens is the note as it is now. */
  function forget() {
    overwriting = false
    settle(false)
    deps.onState({ error: '', dirty: false, ready: false, savedAt: 0, exported: '', exportedLabel: '', edited: [] })
  }

  return {
    handle,
    requestSave,
    saveNow,
    run,
    overwrite,
    forget,
    refreshPieces,
    writePiece,
    pieceNow,
    writeBrought,
    putBack,
    bringAll,
  }
}

/** The pieces the design says it changed, from JSON [[n, first words], …]. */
function editedOf(raw: unknown): { n: string; preview: string }[] {
  try {
    const list = JSON.parse(String(raw)) as unknown
    if (!Array.isArray(list)) return []
    return list
      .filter((x): x is [unknown, unknown] => Array.isArray(x) && x.length >= 2)
      .map(([n, preview]) => ({ n: String(n), preview: String(preview) }))
  } catch {
    return []
  }
}

/** A field of a note's frontmatter, without its quotes: '' without it. */
export function noteField(content: string, key: string): string {
  const fm = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/.exec(content)
  if (!fm) return ''
  const m = new RegExp(`^${key}:[ \\t]*["']?([^"'\\r\\n]*?)["']?[ \\t]*$`, 'm').exec(fm[1] ?? '')
  return m?.[1] ?? ''
}

/** A PDF of a note's design kept beside it: a version's, by its label, or
 *  the draft's (IMP-229). */
export interface NotePdf {
  label: string
  path: string
}
export const DRAFT_PDF = 'draft'

/** The PDFs a note's frontmatter lists in layout_pdfs, each item "label:
 *  path" and quoted, as the server writes them; an older layout_pdf is the
 *  draft's. */
export function notePdfs(content: string): NotePdf[] {
  const fm = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/.exec(content)?.[1] ?? ''
  const line = /^layout_pdfs:[ \t]*(\[.*\])[ \t]*$/m.exec(fm)?.[1]
  let items: unknown = []
  try {
    items = line ? JSON.parse(line) : []
  } catch {
    items = []
  }
  const out: NotePdf[] = []
  for (const item of Array.isArray(items) ? items : []) {
    if (typeof item !== 'string') continue
    const at = item.indexOf(': ')
    if (at > 0 && at < item.length - 2) out.push({ label: item.slice(0, at), path: item.slice(at + 2) })
  }
  const old = noteField(content, 'layout_pdf')
  if (old && !out.some((p) => p.label === DRAFT_PDF)) out.push({ label: DRAFT_PDF, path: old })
  return out
}

/** The type of a note, from its frontmatter: '' without one. */
export function noteTypeOf(content: string): string {
  const type = noteField(content, 'type')
  return /^[\w-]+$/.test(type) ? type : ''
}
