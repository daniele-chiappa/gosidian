import client from './client'

/** The pipelines and their runs (IMP-166, docs/pipelines.md). Every route
 *  answers 404 while the server runs no pipelines
 *  (VersionInfo.pipelines_enabled false). */

export type RunStatus =
  | 'pending'
  | 'queued'
  | 'running'
  | 'waiting'
  | 'done'
  | 'failed'
  | 'skipped'
  | 'canceled'

export interface RunBrief {
  id: string
  pipeline: string
  project: string
  status: RunStatus
  trigger: string
  actor: string
  error?: string
  created: string
  started?: string
  finished?: string
  /** Its steps go before the others' (interactive) or after them (low). */
  interactive?: boolean
  low?: boolean
  /** The category of its pipeline (IMP-196). */
  category?: string
}

export interface PipelineInfo {
  path: string
  title: string
  project: string
  trigger?: string
  /** "id (use)" per node, in order. */
  nodes?: string[]
  /** Why the server would refuse to run it. */
  problem?: string
  /** What keeps it from starting by itself: a commit trigger with git sync off. */
  notice?: string
  last_run?: RunBrief
  /** The category of its runs, and whether the server has rules for it
   *  (IMP-196). */
  category?: string
  category_rules?: boolean
}

export interface StepView {
  name: string
  use?: string
  status: RunStatus
  attempt?: number
  error?: string
  output?: unknown
  output_truncated?: boolean
  started?: string
  finished?: string
  wait_until?: string
  /** What the step takes a place in while it runs (IMP-196). */
  resources?: string[]
  queued_at?: string
  not_before?: string
  /** Why a queued step waits: the steps before it, a full resource. */
  queue?: StepQueue
}

export interface StepQueue {
  ahead: number
  blocked_by?: string
  running?: number
  limit?: number
}

/** The queue of the steps (IMP-196): by resource (plugin:<name>,
 *  llm:<provider>, res:<name>), the steps queued, the places taken of the
 *  whole server and the limit (0: none). */
export interface ResourceQueue {
  resource: string
  queued: number
  running: number
  limit: number
  oldest_wait_seconds: number
}

export interface QueueView {
  queued: number
  later: number
  running: number
  workers: number
  interactive_workers: number
  oldest_wait_seconds: number
  resources: ResourceQueue[]
}

export interface RunDetail {
  run: RunBrief
  steps: StepView[]
}

/** A run that ended: nothing more happens to it. */
export const runEnded = (s: RunStatus): boolean => s === 'done' || s === 'failed' || s === 'canceled'

/** A vault path as URL segments, each escaped. */
const segments = (path: string): string => path.split('/').map(encodeURIComponent).join('/')

export async function listPipelines(project = ''): Promise<PipelineInfo[]> {
  const { data } = await client.get<{ pipelines: PipelineInfo[] }>('/pipelines', {
    params: project ? { project } : {},
  })
  return data.pipelines ?? []
}

export interface RunFilter {
  project?: string
  pipeline?: string
  status?: RunStatus | ''
  limit?: number
}

export async function listRuns(f: RunFilter = {}): Promise<RunBrief[]> {
  const params: Record<string, string | number> = {}
  if (f.project) params.project = f.project
  if (f.pipeline) params.pipeline = f.pipeline
  if (f.status) params.status = f.status
  if (f.limit) params.limit = f.limit
  const { data } = await client.get<{ runs: RunBrief[] | null }>('/runs', { params })
  return data.runs ?? []
}

export async function getQueue(project = ''): Promise<QueueView> {
  const { data } = await client.get<{ queue: QueueView }>('/runs/queue', { params: project ? { project } : {} })
  return data.queue
}

export async function getRun(id: string): Promise<RunDetail> {
  const { data } = await client.get<RunDetail>(`/runs/${encodeURIComponent(id)}`)
  return data
}

/** Starts the pipeline as the signed-in account (trigger manual). */
export async function startRun(path: string, input?: Record<string, unknown>): Promise<RunBrief> {
  const { data } = await client.post<{ run: RunBrief }>(`/pipelines/${segments(path)}/runs`, input ? { input } : {})
  return data.run
}

/** Hands data to the run's waiting step (wait.call). */
export async function resumeRun(id: string, payload: unknown, step = ''): Promise<RunBrief> {
  const { data } = await client.post<{ run: RunBrief }>(`/runs/${encodeURIComponent(id)}/resume`, payload ?? {}, {
    params: step ? { step } : {},
  })
  return data.run
}

export async function cancelRun(id: string): Promise<RunBrief> {
  const { data } = await client.post<{ run: RunBrief }>(`/runs/${encodeURIComponent(id)}/cancel`, {})
  return data.run
}

/** A plugin's status (IMP-168): ok runs its nodes; unaccepted and changed
 *  wait for the owner's `gosidian plugin accept`. */
export type PluginStatus = 'ok' | 'unaccepted' | 'changed' | 'down' | 'unknown'

export interface PluginInfo {
  name: string
  status: PluginStatus
  /** The manifest the plugin serves. */
  version?: string
  sha?: string
  /** The node types of the accepted manifest. */
  nodes: { type: string; description?: string }[]
  /** The sources of the accepted manifest (IMP-172 phase 2), which the
   *  owner binds to IN folders. */
  sources?: { name: string; kind?: string; description?: string; changes?: boolean; move?: boolean }[]
  accepted?: { version: string; sha: string; at: string; by?: string }
  checked?: string
  /** The owner's only: they name hosts of the network. */
  error?: string
  url?: string
}

export async function listPlugins(): Promise<PluginInfo[]> {
  const { data } = await client.get<{ plugins: PluginInfo[] | null }>('/plugins')
  return data.plugins ?? []
}

/** A JSON Schema, as the pipeline editor reads it (IMP-169, F4). */
export type JsonSchema = Record<string, unknown>

/** A node type, with the schemas of its settings and output. */
export interface PipelineNodeType {
  type: string
  /** "base", or the plugin it comes from. */
  group: string
  description?: string
  status?: PluginStatus
  files?: string[]
  input_schema?: JsonSchema
  output_schema?: JsonSchema
}

export interface PipelineCatalog {
  pipelines_enabled: boolean
  nodes: PipelineNodeType[]
  /** The triggers a pipeline may declare, as the server knows them (IMP-207). */
  triggers: string[]
}

/** The node types and triggers for the pipeline editor; answers with the pipelines off too (base types only). */
export async function getPipelineNodes(): Promise<PipelineCatalog> {
  const { data } = await client.get<PipelineCatalog>('/pipeline-nodes')
  return {
    pipelines_enabled: !!data.pipelines_enabled,
    nodes: data.nodes ?? [],
    triggers: data.triggers ?? [],
  }
}

/** Whether the server would run a ```pipeline block not saved yet, and why not. */
export async function checkPipeline(source: string): Promise<{ ok: boolean; problem?: string }> {
  const { data } = await client.post<{ ok: boolean; problem?: string }>('/pipeline-check', { source })
  return data
}

/** A run's steps by node id, as plancia-diagrams' drawing reads them. */
export function runStateOf(d: RunDetail) {
  return {
    id: d.run.id,
    status: d.run.status,
    error: d.run.error,
    steps: Object.fromEntries(
      d.steps.map((s) => [
        s.name,
        {
          status: s.status,
          attempt: s.attempt,
          error: s.error,
          output: s.output,
          outputTruncated: s.output_truncated,
          started: s.started,
          finished: s.finished,
          waitUntil: s.wait_until,
        },
      ]),
    ),
  }
}
