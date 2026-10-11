import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import enUI from '@catalogs/ui.en.json'
import type { PipelineInfo, RunBrief } from '@/api/pipelines'

vi.mock('@/api/pipelines', async (orig) => ({
  ...(await orig<typeof import('@/api/pipelines')>()),
  listPipelines: vi.fn(),
  listPlugins: vi.fn(),
  listRuns: vi.fn(),
  getRun: vi.fn(),
  startRun: vi.fn(),
  resumeRun: vi.fn(),
  cancelRun: vi.fn(),
  getPipelineNodes: async () => ({ pipelines_enabled: true, nodes: [] }),
}))
// The drawing of an open run is plancia-diagrams' (IMP-169, F4): a stand-in
// that shows what it was given.
vi.mock('@/components/domain/diagramView', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    findDiagramBlocks: (await import('plancia-diagrams')).findDiagramBlocks,
    PipelineView: defineComponent({
      props: { source: String, run: Object },
      setup: (p) => () => h('div', { class: 'stand-in', 'data-run': JSON.stringify(p.run) }, p.source),
    }),
  }
})
vi.mock('@/api/notes', () => ({
  getNote: async (path: string) => ({
    path,
    title: 'Copy',
    content: '# Copy\n\n```pipeline\nnodes:\n  - id: ask\n    use: wait.call\n```\n',
    etag: 'e1',
    size: 0,
    mod_time: '',
  }),
}))
vi.mock('@/composables/useSSE', () => ({ useSSE: () => ({ on: vi.fn(() => () => {}) }) }))
vi.mock('@/composables/useConfirm', () => ({ askText: vi.fn() }))
vi.mock('@/stores/access', () => ({
  useAccessStore: () => ({ load: vi.fn(), canWrite: (p: string) => p.startsWith('mine/') }),
}))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ windows: [], open: vi.fn() }),
}))

import { getRun, listPipelines, listPlugins, listRuns, resumeRun, startRun } from '@/api/pipelines'
import { askText } from '@/composables/useConfirm'
import PipelinesView from '@/views/PipelinesView.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

const run = (over: Partial<RunBrief>): RunBrief => ({
  id: 'r1',
  pipeline: 'mine/pipelines/copy.md',
  project: 'mine',
  status: 'done',
  trigger: 'manual',
  actor: 'owner',
  created: '2026-10-10T10:00:00Z',
  ...over,
})

const pipelines: PipelineInfo[] = [
  { path: 'mine/pipelines/copy.md', title: 'Copy', project: 'mine', trigger: 'external', nodes: ['write (note.write)'] },
  { path: 'mine/pipelines/bad.md', title: 'Bad', project: 'mine', problem: 'unknown node type shell.run' },
  { path: 'theirs/pipelines/copy.md', title: 'Theirs', project: 'theirs', trigger: 'manual' },
]

describe('PipelinesView (IMP-166)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(listPipelines).mockResolvedValue(pipelines)
    vi.mocked(listPlugins).mockResolvedValue([])
    vi.mocked(listRuns).mockResolvedValue([
      run({ id: 'r1', status: 'waiting' }),
      run({ id: 'r2', status: 'failed', error: 'boom', pipeline: 'theirs/pipelines/copy.md', project: 'theirs' }),
    ])
    vi.mocked(getRun).mockResolvedValue({
      run: run({ id: 'r1', status: 'waiting' }),
      steps: [{ name: 'ask', use: 'wait.call', status: 'waiting' }, { name: 'write', status: 'pending', output: { path: 'x' } }],
    })
  })

  it('lists the pipelines with their problems and offers Start where the account writes', async () => {
    const w = mount(PipelinesView, { global: { plugins: [i18n()] } })
    await flushPromises()
    const rows = w.find('[data-pipelines]').findAll('li')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.text()).toContain('write (note.write)')
    expect(rows[0]!.text()).toContain('Start')
    expect(rows[1]!.text()).toContain('unknown node type shell.run')
    expect(rows[1]!.text()).not.toContain('Start')
    expect(rows[2]!.text()).not.toContain('Start')
  })

  it('lists no plugins section without plugins, and the plugins with their state and the accept command', async () => {
    let w = mount(PipelinesView, { global: { plugins: [i18n()] } })
    await flushPromises()
    expect(w.find('[data-plugins]').exists()).toBe(false)
    vi.mocked(listPlugins).mockResolvedValue([
      { name: 'docling', status: 'ok', version: '1.0', sha: 'abcdef012345', nodes: [{ type: 'docling.convert' }] },
      { name: 'ocr', status: 'changed', version: '2.0', sha: '0123456789ab', nodes: [{ type: 'ocr.read' }] },
      { name: 'gone', status: 'down', nodes: [], error: 'connection refused' },
    ])
    w = mount(PipelinesView, { global: { plugins: [i18n()] } })
    await flushPromises()
    const rows = w.find('[data-plugins]').findAll('li')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.text()).toContain('running')
    expect(rows[0]!.text()).toContain('docling.convert')
    expect(rows[0]!.text()).not.toContain('gosidian plugin accept')
    expect(rows[1]!.text()).toContain('manifest changed')
    expect(rows[1]!.text()).toContain('gosidian plugin accept ocr 0123456789ab')
    expect(rows[2]!.text()).toContain('unreachable')
    expect(rows[2]!.text()).toContain('connection refused')
  })

  it('works without the list of plugins', async () => {
    vi.mocked(listPlugins).mockRejectedValue(new Error('404'))
    const w = mount(PipelinesView, { global: { plugins: [i18n()] } })
    await flushPromises()
    expect(w.find('[data-pipelines]').exists()).toBe(true)
    expect(w.find('[data-plugins]').exists()).toBe(false)
  })

  it('shows runs with Resume and Cancel for a writer, and a run’s steps on click', async () => {
    const w = mount(PipelinesView, { global: { plugins: [i18n()] } })
    await flushPromises()
    const runs = w.find('[data-runs]').findAll(':scope > li')
    expect(runs[0]!.text()).toContain('waiting')
    expect(runs[0]!.text()).toContain('Resume')
    expect(runs[0]!.text()).toContain('Cancel')
    expect(runs[1]!.text()).toContain('boom')
    expect(runs[1]!.text()).not.toContain('Resume')
    await runs[0]!.find('button[aria-expanded]').trigger('click')
    await flushPromises()
    const steps = w.find('[data-steps]')
    expect(steps.text()).toContain('wait.call')
    expect(steps.text()).toContain('"path": "x"')
  })

  it('starts with the JSON input, and refuses an input that is not an object', async () => {
    vi.mocked(askText).mockResolvedValueOnce('[1]').mockResolvedValueOnce('{"name":"doc"}')
    vi.mocked(startRun).mockResolvedValue(run({ id: 'r3', status: 'queued' }))
    const w = mount(PipelinesView, { global: { plugins: [i18n()] } })
    await flushPromises()
    const start = () => w.find('[data-pipelines]').findAll('li')[0]!.find('button.bg-accent').trigger('click')
    await start()
    await flushPromises()
    expect(startRun).not.toHaveBeenCalled()
    expect(w.text()).toContain('The start data is a JSON object')
    await start()
    await flushPromises()
    expect(startRun).toHaveBeenCalledWith('mine/pipelines/copy.md', { name: 'doc' })
  })

  it('resumes a waiting run with the JSON data', async () => {
    vi.mocked(askText).mockResolvedValueOnce('{"ok":true}')
    vi.mocked(resumeRun).mockResolvedValue(run({ id: 'r1', status: 'running' }))
    const w = mount(PipelinesView, { global: { plugins: [i18n()] } })
    await flushPromises()
    const resume = w.find('[data-runs]').findAll('button').find((b) => b.text().includes('Resume'))!
    await resume.trigger('click')
    await flushPromises()
    expect(resumeRun).toHaveBeenCalledWith('r1', { ok: true })
  })

  it('draws the open run on its pipeline, and opens the pipeline in its editor', async () => {
    const openWindow = vi.fn()
    const w = mount(PipelinesView, { global: { plugins: [i18n()], provide: { openWindow } } })
    await flushPromises()
    await w.find('[data-runs]').find('button[aria-expanded]').trigger('click')
    await vi.waitFor(() => expect(w.find('[data-steps] .stand-in').exists()).toBe(true), { timeout: 10000 })
    expect(w.find('.stand-in').text()).toContain('use: wait.call')
    expect(JSON.parse(w.find('.stand-in').attributes('data-run')!)).toMatchObject({
      id: 'r1',
      status: 'waiting',
      steps: { ask: { status: 'waiting' } },
    })
    const edits = w.findAll('[data-pipeline-open-editor]')
    // Only where the account writes: the two pipelines of mine/.
    expect(edits).toHaveLength(2)
    await edits[0]!.trigger('click')
    expect(openWindow).toHaveBeenCalledWith({
      type: 'pipeline',
      key: 'pipeline:mine/pipelines/copy.md',
      title: 'Pipeline · copy',
      props: { path: 'mine/pipelines/copy.md' },
    })
  })
})
