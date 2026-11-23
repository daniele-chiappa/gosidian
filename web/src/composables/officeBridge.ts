/**
 * The window's side of the bridge office/1 (IMP-233, docs/plugins.md): the
 * office's page in the frame shows a team of agents as a map and asks; the
 * window reads the team with the API, with the rights of whoever is logged
 * in, and opens the notes the page names. The page holds no credentials.
 *
 * Whoever may write the team's project changes it on the map (phase C):
 * the page sends the changes, the window keeps them as a draft, which the
 * server checks at every step, and hands the page the team as the draft
 * makes it. Only the window saves, from its own bar: a page cannot write
 * the team by itself.
 */

export const OFFICE_BRIDGE = 'office/1'

/** maxOps caps a draft, as the server does. */
const MAX_OPS = 200

export interface OfficeMessage {
  data: unknown
  origin: string
  source: unknown
}

export interface OfficeTarget {
  postMessage(message: unknown, targetOrigin: string): void
}

/** A change of the team's block, as POST /api/v1/fleet/teams/{path} takes
 *  it: the server checks the rest. */
export type OfficeOp = Record<string, unknown> & { op: string }

export interface OfficeEditBody {
  etag: string
  ops: OfficeOp[]
  save: boolean
}

/** The answer of POST /api/v1/fleet/teams/{path}: the team as the draft
 *  makes it. The window hands the page what it shows. */
export interface OfficeEditAnswer {
  view: Record<string, unknown>
  block: string
  line: number
  etag: string
  changed: boolean
  creates: string[]
  problems: unknown[]
  saved: boolean
  palette?: unknown
}

/** What the window's bar shows of the draft. error is a code: conflict
 *  (the note changed since the last check), or the server's message. */
export interface OfficeDraftState {
  ops: number
  saving: boolean
  savedAt: number
  error: string
}

export interface OfficeBridgeDeps {
  /** The team's note. */
  path: string
  /** The origin of the app's page: its messages come only from there. */
  readonly origin: string
  frame: () => OfficeTarget | null
  read: (path: string) => Promise<unknown>
  /** edit checks a draft, or saves it. */
  edit?: (path: string, body: OfficeEditBody) => Promise<OfficeEditAnswer>
  open: (path: string) => void
  theme: () => 'light' | 'dark'
  lang: () => string
  errorText: (e: unknown) => string
  /** onDraft hears every change of the draft's state. */
  onDraft?: (s: OfficeDraftState) => void
}

/** openable says whether the page may have the window open path: a note of
 *  the team's project, by a plain path. */
export function openable(path: unknown, project: string): path is string {
  return (
    typeof path === 'string' &&
    path.length < 1024 &&
    path.startsWith(`${project}/`) &&
    /\.md$/i.test(path) &&
    !path.split('/').some((seg) => seg === '' || seg === '.' || seg === '..')
  )
}

/** asOp is a change the page sent, a plain object with its kind: the
 *  server checks its fields. */
function asOp(v: unknown): OfficeOp | null {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return null
  const op = (v as Record<string, unknown>).op
  if (typeof op !== 'string' || !/^[a-z_]{1,32}$/.test(op)) return null
  try {
    const copy = JSON.parse(JSON.stringify(v)) as OfficeOp
    return JSON.stringify(copy).length <= 16_384 ? copy : null
  } catch {
    return null
  }
}

interface ErrorLike {
  response?: { status?: number; data?: { error?: { message?: string; details?: { op?: unknown } } } }
}

function statusOf(e: unknown): number | undefined {
  return (e as ErrorLike)?.response?.status
}

/** refusedOp is the index of the change the server refused, -1 for none. */
function refusedOp(e: unknown): number {
  const op = (e as ErrorLike)?.response?.data?.error?.details?.op
  return statusOf(e) === 422 && typeof op === 'number' && Number.isInteger(op) ? op : -1
}

function serverMessage(e: unknown, fallback: (e: unknown) => string): string {
  return (e as ErrorLike)?.response?.data?.error?.message || fallback(e)
}

export function createOfficeBridge(deps: OfficeBridgeDeps) {
  const project = deps.path.split('/')[0] ?? ''
  let ready = false
  let reading = false
  let again = false
  // The draft (phase C): on while the page edits; its changes, and the
  // version of the note they are checked on.
  let editing = false
  let ops: OfficeOp[] = []
  let etag = ''
  let editable = false
  // The note changed under the draft since it began: the page says so
  // until the draft is saved or thrown away.
  let rebased = false
  const draft: OfficeDraftState = { ops: 0, saving: false, savedAt: 0, error: '' }

  const post = (msg: Record<string, unknown>) => {
    const target = deps.frame()
    if (target && deps.origin) target.postMessage({ bridge: OFFICE_BRIDGE, ...msg }, deps.origin)
  }

  function tell(over: Partial<OfficeDraftState> = {}) {
    Object.assign(draft, over, { ops: ops.length })
    deps.onDraft?.({ ...draft })
  }

  async function send() {
    if (!ready || !deps.frame() || !deps.origin) return
    const base = { theme: deps.theme(), lang: deps.lang() }
    if (!editing || !deps.edit) {
      try {
        const team = (await deps.read(deps.path)) as { etag?: unknown; editable?: unknown }
        etag = typeof team?.etag === 'string' ? team.etag : ''
        editable = team?.editable === true && !!deps.edit
        post({ type: 'team', team, ...base })
      } catch (e) {
        post({ type: 'error', message: deps.errorText(e) })
      }
      return
    }
    // A change the server refuses leaves the draft, with why; the others
    // are checked again without it.
    for (let tries = 0; tries <= ops.length; tries++) {
      try {
        const r = await deps.edit(deps.path, { etag, ops, save: false })
        if (r.changed) rebased = true
        etag = r.etag
        post({
          type: 'team',
          team: r.view,
          ...base,
          draft: {
            editing: true,
            ops: ops.length,
            rebased,
            block: r.block,
            line: r.line,
            creates: r.creates,
            problems: r.problems,
            palette: r.palette,
          },
        })
        return
      } catch (e) {
        const i = refusedOp(e)
        if (i < 0 || i >= ops.length) {
          post({ type: 'error', message: serverMessage(e, deps.errorText) })
          return
        }
        const [op] = ops.splice(i, 1)
        tell()
        post({ type: 'refused', index: i, op, message: serverMessage(e, deps.errorText) })
      }
    }
  }

  /** refresh reads the team and hands it to the page; one read at a time,
   *  and the asks that come during it make one more. */
  async function refresh() {
    if (!ready) return
    if (reading) {
      again = true
      return
    }
    reading = true
    try {
      do {
        again = false
        await send()
      } while (again)
    } finally {
      reading = false
    }
  }

  /** save writes the draft, from the window's bar. */
  async function save(): Promise<boolean> {
    if (!editing || !deps.edit || ops.length === 0 || draft.saving) return false
    tell({ saving: true, error: '' })
    try {
      const r = await deps.edit(deps.path, { etag, ops, save: true })
      ops = []
      rebased = false
      etag = r.etag
      tell({ saving: false, savedAt: Date.now(), error: '' })
      post({ type: 'saved', ok: true, creates: r.creates })
      void refresh()
      return true
    } catch (e) {
      const error = statusOf(e) === 412 ? 'conflict' : serverMessage(e, deps.errorText)
      tell({ saving: false, error })
      post({ type: 'saved', ok: false, error })
      void refresh()
      return false
    }
  }

  /** handle takes a message of the window: true when it was the page's. */
  function handle(e: OfficeMessage): boolean {
    if (!deps.origin || e.origin !== deps.origin || e.source !== deps.frame()) return false
    const d = e.data as Record<string, unknown> | null
    if (!d || typeof d !== 'object' || d.bridge !== OFFICE_BRIDGE) return false
    switch (d.type) {
      case 'ready':
        ready = true
        void refresh()
        break
      case 'refresh':
        void refresh()
        break
      case 'open':
        if (openable(d.path, project)) deps.open(d.path)
        break
      case 'editing':
        // On only where the reader may write; off only without changes:
        // the page throws them away first.
        if (d.on === true && editable && !editing) {
          editing = true
          rebased = false
          void refresh()
        } else if (d.on === false && editing && ops.length === 0) {
          editing = false
          void refresh()
        }
        break
      case 'edit': {
        const op = asOp(d.op)
        if (editing && op && ops.length < MAX_OPS) {
          ops.push(op)
          tell({ error: '' })
          void refresh()
        }
        break
      }
      case 'undo':
        if (editing && ops.length > 0) {
          ops.pop()
          tell({ error: '' })
          void refresh()
        }
        break
      case 'discard':
        if (editing && ops.length > 0) {
          ops = []
          rebased = false
          tell({ error: '' })
          void refresh()
        }
        break
    }
    return true
  }

  return {
    handle,
    refresh,
    save,
    /** forget is a new page coming: it says ready again. The draft stays. */
    forget() {
      ready = false
    },
    get ready() {
      return ready
    },
    get editing() {
      return editing
    },
    get draft(): OfficeDraftState {
      return { ...draft, ops: ops.length }
    },
  }
}

export type OfficeBridge = ReturnType<typeof createOfficeBridge>
