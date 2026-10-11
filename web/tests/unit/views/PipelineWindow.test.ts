import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import { defineComponent, h } from 'vue'
import enUI from '@catalogs/ui.en.json'
import type { RunBrief, RunDetail } from '@/api/pipelines'
// Loaded before the tests: the mocks below import it again, then at once.
import 'plancia-diagrams'

// The editor is plancia-diagrams' (its own tests edit with it): here a
// stand-in that changes the source and shows what it was given, to test
// what the window does around it.
vi.mock('@/components/domain/diagramView', async () => {
  const lib = await import('plancia-diagrams')
  const shown = (p: Record<string, unknown>) =>
    JSON.stringify({ run: p.run ?? null, problems: p.problems ?? [], catalog: p.catalog })
  return {
    findDiagramBlocks: lib.findDiagramBlocks,
    replaceDiagramBlock: lib.replaceDiagramBlock,
    PipelineView: defineComponent({
      props: { source: String, run: Object, catalog: Array },
      setup: (p) => () => h('div', { class: 'view', 'data-shown': shown(p) }, p.source),
    }),
    PipelineEditor: defineComponent({
      props: { source: String, catalog: Array, run: Object, problems: Array, labels: Object, height: Number },
      emits: ['update:source'],
      setup:
        (p, { emit }) =>
        () =>
          h(
            'button',
            {
              class: 'stand-in',
              'data-shown': shown(p),
              onClick: () => emit('update:source', `${p.source}# edited\n`),
            },
            p.source,
          ),
    }),
  }
})

const BLOCK = 'trigger:\n  on: manual\nnodes:\n  - id: a\n    use: note.write\n'
const NOTE = `---\ntitle: Copy\n---\n\nIt copies.\n\n\`\`\`pipeline\n${BLOCK}\`\`\`\n\nAfter.\n`
let remote = { content: NOTE, etag: 'e1' }
const updates: { content: string; ifMatch?: string }[] = []
let refuse = false
vi.mock('@/api/notes', () => ({
  getNote: async (path: string) => ({ path, title: 'Copy', size: 0, mod_time: '', ...remote }),
  updateNote: async (path: string, body: { content: string; ifMatch?: string }) => {
    if (refuse) {
      refuse = false
      throw new AxiosError('412', '412', undefined, undefined, {
        status: 412,
        statusText: 'Precondition Failed',
        data: { error: { code: 'concurrency.etag_mismatch' } },
        headers: {},
        config: { headers: new AxiosHeaders() },
      })
    }
    updates.push(body)
    remote = { content: body.content, etag: `e${updates.length + 1}` }
    return { path, title: 'Copy', size: 0, mod_time: '', ...remote }
  },
}))
vi.mock('@/api/pipelines', async (orig) => ({
  ...(await orig<typeof import('@/api/pipelines')>()),
  getPipelineNodes: vi.fn(),
  checkPipeline: vi.fn(),
  listRuns: vi.fn(),
  getRun: vi.fn(),
  startRun: vi.fn(),
  resumeRun: vi.fn(),
  cancelRun: vi.fn(),
}))
let writable = true
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ canWrite: () => writable }) }))
const handlers: Record<string, (p: Record<string, unknown>) => void> = {}
vi.mock('@/composables/useSSE', () => ({
  useSSE: () => ({
    on: (kind: string, fn: (p: Record<string, unknown>) => void) => {
      handlers[kind] = fn
      return () => {}
    },
  }),
}))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))

import {
  cancelRun,
  checkPipeline,
  getPipelineNodes,
  getRun,
  listRuns,
  resumeRun,
  startRun,
} from '@/api/pipelines'
import { usePipelineCatalog } from '@/composables/pipelineCatalog'
import PipelineWindow from '@/views/PipelineWindow.vue'

const PATH = 'p/pipelines/copy.md'
const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const NODES = [{ type: 'note.write', group: 'base', input_schema: { type: 'object' } }]

const brief = (over: Partial<RunBrief>): RunBrief => ({
  id: 'r1',
  pipeline: PATH,
  project: 'p',
  status: 'done',
  trigger: 'manual',
  actor: 'owner',
  created: '2026-10-11T10:00:00Z',
  ...over,
})
const detail = (b: RunBrief): RunDetail => ({
  run: b,
  steps: [{ name: 'a', use: 'note.write', status: b.status === 'waiting' ? 'waiting' : 'done', attempt: 1 }],
})

const mountWindow = async () => {
  const openWindow = vi.fn()
  const w = mount(PipelineWindow, {
    props: { path: PATH },
    global: { plugins: [i18n()], provide: { openWindow } },
  })
  // Until the note is loaded and the editor (or the note of a missing block) shows.
  await vi.waitFor(
    () => expect(w.find('.stand-in, .view, [data-pipeline-missing]').exists()).toBe(true),
    { timeout: 10000 },
  )
  await settle()
  return { w, openWindow }
}

/** Lets the lazy modules and the awaited calls settle: a few rounds, without counting on time. */
async function settle() {
  for (let i = 0; i < 5; i++) {
    await flushPromises()
    await new Promise((r) => setTimeout(r, 0))
  }
}

const shown = (w: ReturnType<typeof mount>) =>
  JSON.parse(w.find('.stand-in, .view').attributes('data-shown')!)

describe('PipelineWindow (IMP-169, F4)', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    remote = { content: NOTE, etag: 'e1' }
    updates.length = 0
    refuse = false
    writable = true
    vi.mocked(getPipelineNodes).mockResolvedValue({ pipelines_enabled: true, nodes: NODES })
    vi.mocked(checkPipeline).mockResolvedValue({ ok: true })
    vi.mocked(listRuns).mockResolvedValue([])
    vi.mocked(getRun).mockReset()
    vi.mocked(startRun).mockReset()
    vi.mocked(resumeRun).mockReset()
    vi.mocked(cancelRun).mockReset()
    await usePipelineCatalog().reload()
  })

  it('edits the note’s pipeline block and saves it in place, with the etag', async () => {
    const { w } = await mountWindow()
    expect(w.find('.stand-in').element.textContent).toBe(BLOCK)
    expect(shown(w).catalog).toEqual(NODES)
    await w.find('.stand-in').trigger('click')
    expect(w.emitted('dirty')?.at(-1)).toEqual([true])
    expect(w.find('[data-unsaved]').exists()).toBe(true)
    await w.find('[data-pipeline-save]').trigger('click')
    await settle()
    expect(updates).toEqual([
      { content: NOTE.replace(BLOCK, `${BLOCK}# edited\n`), ifMatch: 'e1' },
    ])
    expect(w.emitted('dirty')?.at(-1)).toEqual([false])
    expect(w.emitted('title')?.[0]).toEqual(['Pipeline · copy'])
  })

  it('puts the pipeline in the new version of a note that changed meanwhile', async () => {
    const { w } = await mountWindow()
    await w.find('.stand-in').trigger('click')
    remote = { content: NOTE.replace('It copies.', 'It copies, and more.'), etag: 'e9' }
    refuse = true
    await w.find('[data-pipeline-save]').trigger('click')
    await settle()
    expect(w.find('[data-pipeline-conflict]').exists()).toBe(true)
    await w.find('[data-pipeline-overwrite]').trigger('click')
    await settle()
    expect(updates.at(-1)).toEqual({
      content: NOTE.replace('It copies.', 'It copies, and more.').replace(BLOCK, `${BLOCK}# edited\n`),
      ifMatch: 'e9',
    })
    expect(w.find('[data-pipeline-conflict]').exists()).toBe(false)
  })

  it('shows the server’s problem with the text as it is typed', async () => {
    vi.mocked(checkPipeline).mockResolvedValue({ ok: false, problem: 'node a: unknown node type x.y' })
    const { w } = await mountWindow()
    await w.find('.stand-in').trigger('click')
    await vi.waitFor(
      () => expect(shown(w).problems).toEqual(['node a: unknown node type x.y']),
      { timeout: 3000 },
    )
    expect(checkPipeline).toHaveBeenLastCalledWith(`${BLOCK}# edited\n`)
  })

  it('says when the note has no pipeline block', async () => {
    remote = { content: '# Not a pipeline\n', etag: 'e1' }
    const { w } = await mountWindow()
    expect(w.find('[data-pipeline-missing]').exists()).toBe(true)
  })

  it('draws the note for who may only read it', async () => {
    writable = false
    const { w } = await mountWindow()
    expect(w.find('.view').element.textContent).toBe(BLOCK)
    expect(w.find('[data-pipeline-save]').exists()).toBe(false)
    expect(w.find('[data-run-start]').exists()).toBe(false)
  })

  it('edits with the pipelines off, and says they do not run', async () => {
    vi.mocked(getPipelineNodes).mockResolvedValue({ pipelines_enabled: false, nodes: NODES })
    await usePipelineCatalog().reload()
    const { w } = await mountWindow()
    expect(w.find('[data-pipelines-off]').exists()).toBe(true)
    expect(w.find('[data-pipeline-runs]').exists()).toBe(false)
    expect(listRuns).not.toHaveBeenCalled()
    expect(w.find('.stand-in').exists()).toBe(true)
  })

  it('draws the last run, starts one with its input and follows the run’s events', async () => {
    const done = brief({ id: 'r1', status: 'done' })
    vi.mocked(listRuns).mockResolvedValue([done])
    vi.mocked(getRun).mockImplementation(async (id) =>
      detail(id === 'r2' ? brief({ id: 'r2', status: 'running' }) : done),
    )
    const { w } = await mountWindow()
    expect(listRuns).toHaveBeenCalledWith({ pipeline: PATH, limit: 10 })
    expect(shown(w).run).toMatchObject({ id: 'r1', status: 'done', steps: { a: { status: 'done', attempt: 1 } } })

    await w.find('[data-run-start]').trigger('click')
    await w.find('[data-run-input]').setValue('{"name": ')
    await w.find('[data-run-go]').trigger('click')
    await settle()
    expect(startRun).not.toHaveBeenCalled()
    expect(w.text()).toContain('Not valid JSON')

    vi.mocked(startRun).mockResolvedValue(brief({ id: 'r2', status: 'queued' }))
    vi.mocked(listRuns).mockResolvedValue([brief({ id: 'r2', status: 'running' }), done])
    await w.find('[data-run-input]').setValue('{"name": "x"}')
    await w.find('[data-run-go]').trigger('click')
    await settle()
    expect(startRun).toHaveBeenCalledWith(PATH, { name: 'x' })
    expect(w.find('[data-run-form]').exists()).toBe(false)
    expect(shown(w).run).toMatchObject({ id: 'r2', status: 'running' })
    expect(w.find('[data-run-cancel]').exists()).toBe(true)

    // The run ends: its job event brings the new state.
    vi.mocked(listRuns).mockResolvedValue([brief({ id: 'r2', status: 'done' }), done])
    vi.mocked(getRun).mockResolvedValue(detail(brief({ id: 'r2', status: 'done' })))
    handlers.job!({ pipeline: 'p/pipelines/other.md' })
    handlers.job!({ pipeline: PATH })
    await vi.waitFor(() => expect(shown(w).run).toMatchObject({ id: 'r2', status: 'done' }), {
      timeout: 3000,
    })
    expect(w.find('[data-run-cancel]').exists()).toBe(false)
  })

  it('does not start a run of a pipeline not saved yet', async () => {
    const { w } = await mountWindow()
    await w.find('.stand-in').trigger('click')
    await w.find('[data-run-start]').trigger('click')
    expect(w.find('[data-run-form]').text()).toContain('Save')
    expect(w.find('[data-run-go]').attributes('disabled')).toBeDefined()
  })

  it('resumes a waiting run with its data, and cancels one', async () => {
    const waiting = brief({ id: 'r1', status: 'waiting' })
    vi.mocked(listRuns).mockResolvedValue([waiting])
    vi.mocked(getRun).mockResolvedValue(detail(waiting))
    vi.mocked(resumeRun).mockResolvedValue(waiting)
    vi.mocked(cancelRun).mockResolvedValue(undefined as never)
    const { w } = await mountWindow()
    await w.find('[data-run-resume]').trigger('click')
    await w.find('[data-run-input]').setValue('{"ok": true}')
    await w.find('[data-run-go]').trigger('click')
    await settle()
    expect(resumeRun).toHaveBeenCalledWith('r1', { ok: true })
    await w.find('[data-run-cancel]').trigger('click')
    await settle()
    expect(cancelRun).toHaveBeenCalledWith('r1')
  })

  it('reloads on a change made elsewhere, and keeps a draft', async () => {
    const { w } = await mountWindow()
    remote = { content: NOTE.replace('note.write', 'note.append'), etag: 'e5' }
    handlers.note!({ path: PATH, etag: '"e1"' })
    await settle()
    expect(w.find('.stand-in').element.textContent).toBe(BLOCK)
    handlers.note!({ path: PATH, etag: '"e5"' })
    await settle()
    expect(w.find('.stand-in').text()).toContain('note.append')
    await w.find('.stand-in').trigger('click')
    remote = { content: NOTE, etag: 'e6' }
    handlers.note!({ path: PATH, etag: '"e6"' })
    await settle()
    expect(w.find('.stand-in').text()).toContain('note.append')
    expect(w.text()).toContain('changed')
  })
})
