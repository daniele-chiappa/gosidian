/**
 * Host-side window keying + URL codec for gosidian's plancia.
 *
 * plancia's `usePlanciaSync(codec, options)` takes a pluggable `PlanciaCodec`;
 * the library no longer owns gosidian's per-type token scheme. This module:
 *
 *  - re-homes `planciaKey` (the stable, decoded de-dup key used across the app
 *    when opening windows) and `base` (note-path → display title) host-side,
 *    since plancia doesn't export them; and
 *  - builds the gosidian `codec`, mostly via the library's `createArgCodec`,
 *    with one bespoke wrap: the legacy `edit:<path>` deep-link decodes to a
 *    **note** window in edit mode, keyed/normalised as `note:<path>` so the URL
 *    collapses to `note:` and the window de-dups against an open read window.
 *
 * The token scheme (unchanged from the previous inlined composable): a token is
 * either the bare type (singleton) or `type:<encodeURIComponent(arg)>`; parsing
 * splits on the FIRST `:` so an encoded path (no raw `:`) round-trips cleanly.
 */
import { createArgCodec, type ArgTypeSpec, type PlanciaCodec, type WindowInstance } from 'plancia'
import { i18n } from '@/locales'

// Window titles in the user's language (IMP-155, M2), read when a window
// is rebuilt from the address; in another one while retitle asks for it.
// The catalogue types know only it and en; de, es and fr come when chosen.
type Lang = typeof i18n.global.locale.value
let titleLocale: Lang | undefined
const tr = (key: string, named: Record<string, unknown> = {}) =>
  i18n.global.t(key, named, { locale: titleLocale ?? i18n.global.locale.value })
// A section the catalogue does not know keeps its own name.
const trOr = (key: string, fallback: string) =>
  i18n.global.te(key, titleLocale) ? tr(key) : fallback
import { queryArgFromProps, queryPropsFromArg } from './queryArg'

const str = (v: unknown): string | null => (typeof v === 'string' && v ? v : null)

/** Note path → display title: last segment without the `.md` or `.html` suffix. */
export const base = (p: string): string => (p.split('/').pop() ?? p).replace(/\.(md|html)$/i, '')

/** Note path → its deep link, `/notes/<path>` with each segment escaped. */
export const noteHref = (p: string): string => '/notes/' + p.split('/').map(encodeURIComponent).join('/')

/** Stable de-dup key (decoded). Use this when opening windows from the app so a
 *  hydrated window de-dups against a later open of the same target. Mirrors the
 *  codec's own `key()` so host opens and URL hydration agree. */
export function planciaKey(type: string, arg?: string | null): string {
  return arg ? `${type}:${arg}` : type
}

/** Windows whose URL arg is the note path, with `base()` as the title. */
const PATH_TYPE = (): ArgTypeSpec => ({
  arg: (p) => str(p.path),
  props: (a) => (a ? { path: a } : {}),
  title: (a) => (a ? base(a) : ''),
})

/**
 * Graph window arg — two wire forms:
 *  - legacy bare focus path (ego windows from "direct links"): stable URLs,
 *    unchanged since v2.x;
 *  - compact param-string (`p=…&t=…&f=…&d=…&m=…&l=…`) for the filtered global
 *    view, so filters survive reloads. `global: true` in props marks the
 *    full view (aside visible) and forces the param form even when only a
 *    focus filter is set.
 */
const posNum = (v: unknown): number | null =>
  typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : null

function graphArgFromProps(p: Record<string, unknown>): string | null {
  const project = str(p.project)
  const tag = str(p.tag)
  const focus = str(p.focus)
  const depth = posNum(p.depth)
  const minDegree = posNum(p.minDegree)
  const limit = posNum(p.limit)
  if (p.global !== true && focus && !project && !tag && !minDegree && !limit && (depth ?? 1) <= 1)
    return focus
  const parts: string[] = []
  // `p=*`: every project, chosen by hand (BUG-158). Without it a graph with
  // no project opens on the most recent one.
  if (project) parts.push('p=' + encodeURIComponent(project))
  else if (p.allProjects === true) parts.push('p=*')
  if (tag) parts.push('t=' + encodeURIComponent(tag))
  if (focus) parts.push('f=' + encodeURIComponent(focus))
  if (focus && depth !== null && depth !== 2) parts.push('d=' + depth)
  if (minDegree !== null) parts.push('m=' + minDegree)
  if (limit !== null) parts.push('l=' + limit)
  return parts.length ? parts.join('&') : null
}

function graphPropsFromArg(a: string | null): Record<string, unknown> {
  if (!a) return {}
  if (!a.includes('=')) return { focus: a, depth: 1 }
  const sp = new URLSearchParams(a)
  const props: Record<string, unknown> = { global: true }
  const p = sp.get('p')
  if (p === '*') props.allProjects = true
  else if (p) props.project = p
  const t = sp.get('t')
  if (t) props.tag = t
  const f = sp.get('f')
  if (f) props.focus = f
  const d = Number(sp.get('d'))
  if (f) props.depth = d > 0 ? d : 2
  const m = Number(sp.get('m'))
  if (m > 0) props.minDegree = m
  const l = Number(sp.get('l'))
  if (l > 0) props.limit = l
  return props
}

/**
 * De-dup key for a decoded graph window, mirroring the opener conventions:
 * project-anchored views reuse ProjectsView's `graph:project:<name>` key so
 * they survive reloads as distinct windows; everything else is the sidebar
 * singleton `graph`. Ego (bare-path) tokens never reach this — their key is
 * the base codec's `graph:<path>`.
 */
function graphKeyForProps(props: Record<string, unknown>): string {
  const project = str(props.project)
  return project ? `graph:project:${project}` : planciaKey('graph')
}

/**
 * Creation window arg (BUG-154): `<folder>/<name>` from a missing note's
 * link, `<folder>/` from a folder's +. A name has no `/`, so the last one
 * splits them.
 */
function createArgFromProps(p: Record<string, unknown>): string | null {
  const folder = str(p.path) ?? ''
  const name = str(p.name)
  if (name) return `${folder}/${name}`
  return folder ? `${folder}/` : null
}

function createPropsFromArg(a: string | null): Record<string, unknown> {
  if (!a) return {}
  const i = a.lastIndexOf('/')
  if (i < 0) return { path: a }
  const name = a.slice(i + 1)
  return name ? { path: a.slice(0, i), name } : { path: a.slice(0, i) }
}

/** The key the openers give a creation window: the folder, or the note
 *  to create when there is a name. */
function createKeyForProps(props: Record<string, unknown>): string {
  const folder = str(props.path) ?? ''
  const name = str(props.name)
  return planciaKey('create', name ? `${folder}/${name}` : folder)
}

/** gosidian's per-type URL scheme, expressed for `createArgCodec`. */
const types: Record<string, ArgTypeSpec> = {
  note: PATH_TYPE(),
  edit: PATH_TYPE(),
  history: { ...PATH_TYPE(), title: (a) => (a ? `${tr('history.title')} · ${base(a)}` : '') },
  // A diagram of a note (IMP-169): `<path>#<index>`, the index of its
  // block in the note's text.
  diagram: {
    arg: (p) => (str(p.path) && Number.isInteger(p.index) ? `${p.path}#${p.index}` : null),
    props: (a) => {
      const i = a?.lastIndexOf('#') ?? -1
      return a && i > 0 ? { path: a.slice(0, i), index: Number(a.slice(i + 1)) || 0 } : {}
    },
    title: (a) => (a ? tr('diagram.window_title', { name: base(a.slice(0, a.lastIndexOf('#'))) }) : ''),
  },
  // A pipeline note in the pipeline editor (IMP-169, F4): its path.
  pipeline: {
    arg: (p) => str(p.path) || null,
    props: (a) => (a ? { path: a } : {}),
    title: (a) => (a ? tr('pipeline_editor.window_title', { name: base(a) }) : ''),
  },
  // A note in the app a plugin offers for its type (IMP-226): its path.
  app: {
    arg: (p) => str(p.path) || null,
    props: (a) => (a ? { path: a } : {}),
    title: (a) => (a ? tr('plugin_app.window_title', { name: base(a) }) : ''),
  },
  // A project's IN folder (IMP-172): the project.
  in: {
    arg: (p) => str(p.project) || null,
    props: (a) => (a ? { project: a } : {}),
    title: (a) => (a ? tr('in.window_title', { name: a }) : ''),
  },
  // Who has access to a project (BUG-154): the project.
  'project-members': {
    arg: (p) => str(p.project),
    props: (a) => (a ? { project: a } : {}),
    title: (a) => (a ? tr('members.title', { project: a }) : ''),
  },
  // A new note (IMP-058, BUG-154): the folder, and the name when it
  // comes from a missing note's link.
  create: {
    arg: createArgFromProps,
    props: createPropsFromArg,
    title: (a) => {
      const folder = String(createPropsFromArg(a).path ?? '')
      return tr('note_create.window_title', { name: folder.split('/').pop() || '/' })
    },
  },
  graph: {
    arg: graphArgFromProps,
    props: graphPropsFromArg,
    title: (a) => {
      if (!a) return tr('nav.graph')
      if (!a.includes('=')) return tr('graph.links_title', { name: base(a) })
      const p = new URLSearchParams(a).get('p')
      return p && p !== '*' ? `${tr('nav.graph')} · ${p}` : tr('nav.graph')
    },
  },
  tags: {
    arg: (p) => str(p.tag),
    props: (a) => (a ? { tag: a } : {}),
    title: (a) => (a ? `#${a}` : tr('nav.tags')),
  },
  // The query window keeps its whole query in the URL (queryArg.ts); it
  // stays one window, keyed `query`, whatever the query.
  query: {
    arg: queryArgFromProps,
    props: queryPropsFromArg,
    title: () => tr('nav.query'),
  },
  // The admin is one window (IMP-224): its section, and after a `?` the
  // audit's filter (auditFilter.ts), which the window sets as they change.
  admin: {
    arg: (p) => {
      const section = str(p.section)
      return section && str(p.audit) ? `${section}?${p.audit}` : section
    },
    props: (a) => {
      const i = a?.indexOf('?') ?? -1
      return !a ? {} : i < 0 ? { section: a } : { section: a.slice(0, i), audit: a.slice(i + 1) }
    },
    title: (a) => {
      const section = a?.split('?')[0]
      return section ? `${tr('nav.admin')} · ${trOr(`admin.tab.${section}`, section)}` : tr('nav.admin')
    },
  },
  // Singletons with a fixed display title.
  search: { arg: () => null, title: () => tr('nav.search') },
  projects: { arg: () => null, title: () => tr('nav.projects') },
  settings: { arg: () => null, title: () => tr('nav.settings') },
  trash: { arg: () => null, title: () => tr('nav.trash') },
  runs: { arg: () => null, title: () => tr('nav.runs') },
  uploads: { arg: () => null, title: () => tr('uploads.window_title') },
}

/** The window types the codec knows: any other comes back from the
 *  address as a bare singleton, without its props (BUG-154). */
export const codecTypes: readonly string[] = Object.keys(types)

const baseCodec: PlanciaCodec = createArgCodec({
  bareToken: 'type',
  types,
  // Unlisted types are bare singletons titled by their type.
  default: { arg: () => null },
})

/**
 * gosidian codec: `createArgCodec` for the standard types, plus a bespoke
 * `decode` that normalises the legacy `edit:<path>` deep-link to a `note`
 * window in edit mode. `encode`/`key` delegate to the base codec unchanged — a
 * window's `type` is always `note` once open, so it always serialises to
 * `note:<path>` (the `edit:` token only ever appears as an inbound deep-link).
 */
export const codec: PlanciaCodec = {
  key(spec) {
    // Full graph views re-anchor to the opener-convention key (see
    // graphKeyForProps); keep key() aligned with decode() so URL
    // hydration de-dups against the same identity.
    if (spec.type === 'graph' && spec.props?.global === true) {
      return graphKeyForProps(spec.props)
    }
    if (spec.type === 'query' || spec.type === 'admin') return planciaKey(spec.type)
    if (spec.type === 'create') return createKeyForProps(spec.props ?? {})
    return baseCodec.key(spec)
  },
  encode: baseCodec.encode,
  decode(token) {
    const spec = baseCodec.decode(token)
    if (spec?.type === 'edit') {
      const path = str(spec.props?.path)
      return {
        type: 'note',
        key: planciaKey('note', path),
        title: path ? base(path) : '',
        props: path ? { path, mode: 'edit' } : {},
      }
    }
    if (spec?.type === 'graph' && spec.props?.global === true) {
      return { ...spec, key: graphKeyForProps(spec.props) }
    }
    if (spec?.type === 'query' || spec?.type === 'admin') return { ...spec, key: planciaKey(spec.type) }
    if (spec?.type === 'create') return { ...spec, key: createKeyForProps(spec.props ?? {}) }
    return spec
  },
}

/** The title the address gives a window, in lang. */
function addressTitle(win: WindowInstance, lang: string): string {
  titleLocale = lang as Lang
  try {
    const token = codec.encode(win)
    return (token && codec.decode(token)?.title) || ''
  } finally {
    titleLocale = undefined
  }
}

/** The kind of a «kind · name» title, with its separator; '' for another. */
const kind = (title: string): string => {
  const i = title.indexOf(' · ')
  return i < 0 ? '' : title.slice(0, i + 3)
}

/**
 * A window's title in the language `to`, or null to keep it (BUG-155): the
 * plancia keeps titles as text, translated when the window opened. A title
 * the address gives in `from` is given again in `to`; a «kind · name» one
 * keeps its name, the note's title in the links and the history, and
 * changes its kind. A title a view gave, the title of a note, is kept.
 */
export function retitle(win: WindowInstance, from: string, to: string): string | null {
  const was = addressTitle(win, from)
  if (!was) return null
  if (win.title === was) return addressTitle(win, to)
  const k = kind(was)
  return k && win.title.startsWith(k)
    ? kind(addressTitle(win, to)) + win.title.slice(k.length)
    : null
}
