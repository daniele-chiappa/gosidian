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
  last_run?: RunBrief
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
