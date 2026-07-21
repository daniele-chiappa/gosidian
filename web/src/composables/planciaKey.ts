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
import { createArgCodec, type ArgTypeSpec, type PlanciaCodec } from 'plancia'

const str = (v: unknown): string | null => (typeof v === 'string' && v ? v : null)

/** Note path → display title: last segment without the `.md` suffix. */
export const base = (p: string): string => (p.split('/').pop() ?? p).replace(/\.md$/, '')

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
  if (project) parts.push('p=' + encodeURIComponent(project))
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
  if (p) props.project = p
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

/** gosidian's per-type URL scheme, expressed for `createArgCodec`. */
const baseCodec: PlanciaCodec = createArgCodec({
  bareToken: 'type',
  types: {
    note: PATH_TYPE(),
    edit: PATH_TYPE(),
    history: PATH_TYPE(),
    graph: {
      arg: graphArgFromProps,
      props: graphPropsFromArg,
      title: (a) => {
        if (!a) return 'Graph'
        if (!a.includes('=')) return `↳ ${base(a)}`
        const p = new URLSearchParams(a).get('p')
        return p ? `Graph · ${p}` : 'Graph'
      },
    },
    tags: {
      arg: (p) => str(p.tag),
      props: (a) => (a ? { tag: a } : {}),
      title: (a) => (a ? `#${a}` : 'Tags'),
    },
    admin: {
      arg: (p) => str(p.section),
      props: (a) => (a ? { section: a } : {}),
      title: (a) => (a ? `Admin · ${a}` : 'Admin'),
    },
    // Singletons with a fixed display title.
    search: { arg: () => null, title: () => 'Search' },
    projects: { arg: () => null, title: () => 'Projects' },
    settings: { arg: () => null, title: () => 'Settings' },
    trash: { arg: () => null, title: () => 'Trash' },
  },
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
    return spec
  },
}
