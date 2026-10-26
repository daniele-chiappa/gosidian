/**
 * Uploads store — the uploads into the IN folders (IMP-172 phase 5), which
 * go on while the windows change: three files at a time over all of them,
 * each in pieces that follow the line's speed (1 to 32 MiB, a piece taking
 * a few seconds), with pause, resume and a retry per file. A piece lost on
 * the way is sent again from the offset the server has (409 says it); a
 * network error waits and tries again; a full disk (507) pauses the
 * upload. When every file has arrived the store completes the upload and
 * follows the server's completion to its results.
 *
 * A reload loses the files (the browser gives them only once): the server
 * keeps the bytes, and choosing the same files again resumes the upload
 * (resumeWith, or the same files opened into the same folder).
 * reset, at sign-out, stops everything.
 */
import { defineStore } from 'pinia'
import axios from 'axios'
import {
  cancelUpload,
  completeUpload,
  getUpload,
  openUpload,
  putPiece,
  type UploadFileResult,
  type UploadInfo,
  type UploadPolicy,
} from '@/api/inUpload'

/** Files sent at the same time, over every upload. */
export const PARALLEL = 3
export const MIN_PIECE = 1 << 20
export const MAX_PIECE = 32 << 20
/** A piece that takes less grows, one that takes more shrinks. */
const FAST_MS = 3000
const SLOW_MS = 15000
/** The waits between the tries of a piece the network lost. */
export const RETRY_DELAYS_MS = [1000, 2000, 4000, 8000, 16000, 30000]
/** The completion is followed this often. */
const POLL_MS = 1000

export type JobState = 'opening' | 'asking' | 'sending' | 'paused' | 'completing' | 'done' | 'error'
export type ItemState = 'waiting' | 'sending' | 'paused' | 'sent' | 'error' | 'dropped' | 'missing'

/** A file chosen to upload, with its path under the upload's folder. */
export interface PickedFile {
  file: File
  path: string
  unzip?: boolean
}

export interface UploadItem {
  n: number
  path: string
  size: number
  unzip: boolean
  received: number
  /** Bytes of the piece on its way. */
  inflight: number
  state: ItemState
  /** Why it stopped: an error of a request, for errorText. */
  error?: unknown
  exists: boolean
  result?: UploadFileResult
}

export interface UploadJob {
  /** The server's id ('' while it opens). */
  id: string
  key: string
  project: string
  dir: string
  policy: UploadPolicy
  state: JobState
  error?: unknown
  /** The server said why its completion stopped. */
  serverError?: string
  /** The disk is full: paused by the server, not by the user. */
  full?: boolean
  items: UploadItem[]
  bytes: number
  piece: number
  /** Bytes per second, smoothed. */
  rate: number
  started: number
}

// What is not state: the files, the requests on their way, the timers.
const files = new Map<string, File>()
const controllers = new Map<string, AbortController>()
const timers = new Set<ReturnType<typeof setTimeout>>()
let seq = 0
let epoch = 0

const fileKey = (job: UploadJob, n: number) => `${job.key}/${n}`

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(() => {
      timers.delete(t)
      resolve()
    }, ms)
    timers.add(t)
  })
}

/** The bytes the server has, when a piece's answer says them. */
function receivedOf(e: unknown): number | undefined {
  if (!axios.isAxiosError(e) || e.response?.status !== 409) return undefined
  const r = (e.response.data as { error?: { details?: { received?: unknown } } })?.error?.details?.received
  return typeof r === 'number' ? r : undefined
}

function statusOf(e: unknown): number {
  return axios.isAxiosError(e) ? (e.response?.status ?? 0) : -1
}

const aborted = (e: unknown) =>
  axios.isCancel(e) || (axios.isAxiosError(e) && e.code === 'ERR_CANCELED') || (e instanceof DOMException && e.name === 'AbortError')

/**
 * pathProblem says why IN would refuse a path, '' when it takes it: the
 * server's rules (no hidden, empty, . or .. segment, no \ or control
 * character, a segment of at most 255 bytes without spaces around it),
 * checked here so that a folder with a .DS_Store is not refused whole.
 */
export function pathProblem(path: string): '' | 'hidden' | 'name' {
  const segs = path.split('/')
  if (segs.some((s) => s.startsWith('.') && s !== '.' && s !== '..')) return 'hidden'
  const enc = new TextEncoder()
  // eslint-disable-next-line no-control-regex
  if (!path || path.length > 1024 || /[\\\u0000-\u001f\u007f]/.test(path)) return 'name'
  for (const s of segs) {
    if (!s || s === '.' || s === '..' || s.trim() !== s || enc.encode(s).length > 255) return 'name'
  }
  return ''
}

export const useUploadsStore = defineStore('uploads', {
  state: () => ({ jobs: [] as UploadJob[] }),

  getters: {
    /** Something is on its way: closing the tab would stop it. */
    busy: (s) => s.jobs.some((j) => j.state === 'opening' || j.state === 'sending' || j.state === 'completing'),
    active: (s) => s.jobs.filter((j) => j.state !== 'done'),
    /** Bytes sent and to send, over the uploads not done. */
    progress: (s) => {
      let done = 0
      let total = 0
      for (const j of s.jobs) {
        if (j.state === 'done') continue
        for (const it of j.items) {
          if (it.state === 'dropped') continue
          total += it.size
          done += Math.min(it.size, it.received + it.inflight)
        }
      }
      return { done, total }
    },
  },

  actions: {
    job(key: string): UploadJob | undefined {
      return this.jobs.find((j) => j.key === key)
    },

    /** Opens an upload of files into a folder of a project's IN folder. */
    async add(project: string, dir: string, picked: PickedFile[]): Promise<UploadJob | undefined> {
      if (!picked.length) return undefined
      const mine = epoch
      const job: UploadJob = {
        id: '', key: `u${++seq}`, project, dir, policy: 'keep', state: 'opening', items: [],
        bytes: picked.reduce((a, p) => a + p.file.size, 0), piece: 8 << 20, rate: 0, started: Date.now(),
      }
      this.jobs.unshift(job)
      const live = this.jobs[0]!
      try {
        const u = await openUpload(project, dir, picked.map((p) => ({
          path: p.path, size: p.file.size, unzip: p.unzip || undefined, modified: p.file.lastModified || undefined,
        })))
        if (mine !== epoch) return undefined
        const twin = this.jobs.find((j) => j.id === u.id)
        if (twin) {
          // The same files again, while this tab sends them: that upload.
          this.jobs = this.jobs.filter((j) => j !== live)
          return twin
        }
        this.attach(live, u, picked)
        const taken = live.items.some((it) => it.exists && it.received < it.size)
        live.state = taken ? 'asking' : 'sending'
        if (!taken) this.pump()
      } catch (e) {
        if (mine !== epoch) return undefined
        live.state = 'error'
        live.error = e
      }
      return live
    },

    /** Takes an upload of the server, with the files chosen again for it:
     *  a file matches by path and size; one not chosen again, and not
     *  whole on the server, waits for them. */
    async resumeWith(project: string, id: string, picked: PickedFile[]): Promise<UploadJob> {
      const u = await getUpload(project, id)
      let live = this.jobs.find((j) => j.id === id)
      if (!live) {
        this.jobs.unshift({
          id, key: `u${++seq}`, project, dir: u.dir, policy: u.policy, state: 'paused', items: [],
          bytes: u.bytes, piece: u.chunk_size, rate: 0, started: Date.now(),
        })
        live = this.jobs[0]!
      }
      this.attach(live, u, picked)
      live.state = u.state === 'open' ? 'sending' : 'completing'
      if (u.state === 'open') this.pump()
      else void this.follow(live)
      return live
    },

    /** Fills a job with the files of its upload on the server. */
    attach(job: UploadJob, u: UploadInfo, picked: PickedFile[]) {
      job.id = u.id
      job.policy = u.policy
      job.piece = Math.min(MAX_PIECE, Math.max(MIN_PIECE, u.chunk_size || job.piece))
      const byPath = new Map(picked.map((p) => [p.path.normalize('NFC'), p]))
      job.items = (u.files ?? []).map((f) => {
        const p = byPath.get(f.path)
        const has = p && p.file.size === f.size
        if (has) files.set(fileKey(job, f.n), p.file)
        const whole = f.received >= f.size
        return {
          n: f.n, path: f.path, size: f.size, unzip: !!f.unzip, received: f.received, inflight: 0,
          state: f.dropped ? 'dropped' : whole ? 'sent' : has ? 'waiting' : 'missing',
          exists: !!f.exists, result: f.result,
        } satisfies UploadItem
      })
      job.bytes = job.items.reduce((a, it) => a + it.size, 0)
    },

    /** The choice for the files whose place is taken; skip leaves them
     *  unsent (a zip goes, its files there are skipped one by one). */
    choose(key: string, policy: UploadPolicy) {
      const job = this.job(key)
      if (!job || job.state !== 'asking') return
      job.policy = policy
      if (policy === 'skip') {
        for (const it of job.items) if (it.exists && !it.unzip && it.state !== 'sent') it.state = 'dropped'
      }
      job.state = 'sending'
      this.pump()
      this.maybeComplete(job)
    },

    pause(key: string) {
      const job = this.job(key)
      if (!job || (job.state !== 'sending' && job.state !== 'asking')) return
      job.state = 'paused'
      for (const it of job.items) {
        if (it.state === 'sending') controllers.get(fileKey(job, it.n))?.abort()
        if (it.state === 'waiting' || it.state === 'sending') it.state = 'paused'
      }
    },

    resume(key: string) {
      const job = this.job(key)
      if (!job || (job.state !== 'paused' && job.state !== 'error')) return
      job.full = false
      job.error = undefined
      if (job.state === 'error' && job.items.every((it) => it.state === 'sent' || it.state === 'dropped') && job.id) {
        // The completion stopped: again.
        void this.complete(job)
        return
      }
      for (const it of job.items) {
        if (it.state === 'paused' || it.state === 'error') {
          it.state = files.has(fileKey(job, it.n)) ? 'waiting' : 'missing'
          it.error = undefined
        }
      }
      job.state = job.id ? 'sending' : 'error'
      this.pump()
      this.maybeComplete(job)
    },

    retry(key: string, n: number) {
      const job = this.job(key)
      const it = job?.items.find((i) => i.n === n)
      if (!job || !it || it.state !== 'error') return
      it.state = 'waiting'
      it.error = undefined
      if (job.state === 'error' || job.state === 'paused') job.state = 'sending'
      this.pump()
    },

    /** Leaves a file out of its upload. */
    drop(key: string, n: number) {
      const job = this.job(key)
      const it = job?.items.find((i) => i.n === n)
      if (!job || !it || it.state === 'sent' || job.state === 'completing' || job.state === 'done') return
      controllers.get(fileKey(job, n))?.abort()
      it.state = 'dropped'
      it.inflight = 0
      this.maybeComplete(job)
    },

    /** Drops an upload, on the server too. */
    async cancel(key: string) {
      const job = this.job(key)
      if (!job) return
      for (const it of job.items) controllers.get(fileKey(job, it.n))?.abort()
      this.forget(job)
      if (job.id && job.state !== 'done') {
        try {
          await cancelUpload(job.project, job.id)
        } catch {
          /* gone already, or completing: the server's old uploads go by themselves */
        }
      }
    },

    clearDone() {
      for (const j of this.jobs.filter((j) => j.state === 'done')) this.forget(j)
    },

    forget(job: UploadJob) {
      for (const it of job.items) {
        files.delete(fileKey(job, it.n))
        controllers.delete(fileKey(job, it.n))
      }
      this.jobs = this.jobs.filter((j) => j.key !== job.key)
    },

    /** Starts files while fewer than PARALLEL are on their way. */
    pump() {
      let running = 0
      for (const j of this.jobs) for (const it of j.items) if (it.state === 'sending') running++
      for (const j of [...this.jobs].reverse()) {
        if (j.state !== 'sending') continue
        for (const it of j.items) {
          if (running >= PARALLEL) return
          if (it.state !== 'waiting') continue
          if (it.size === 0 || it.received >= it.size) {
            it.state = 'sent'
            this.maybeComplete(j)
            continue
          }
          running++
          it.state = 'sending'
          void this.send(j, it)
        }
      }
    },

    /** Sends a file's pieces, from the bytes the server has. */
    async send(job: UploadJob, it: UploadItem) {
      const mine = epoch
      const key = fileKey(job, it.n)
      const file = files.get(key)
      let tries = 0
      while (file && it.state === 'sending' && it.received < it.size) {
        const size = Math.min(job.piece, it.size - it.received)
        const ctl = new AbortController()
        controllers.set(key, ctl)
        const t0 = performance.now()
        try {
          const r = await putPiece(job.project, job.id, it.n, it.received, file.slice(it.received, it.received + size), {
            signal: ctl.signal,
            onProgress: (loaded) => (it.inflight = Math.min(loaded, size)),
          })
          if (mine !== epoch) return
          const ms = performance.now() - t0
          const got = r.received - it.received
          it.received = r.received
          it.inflight = 0
          tries = 0
          if (got > 0 && ms > 0) {
            const rate = (got * 1000) / ms
            job.rate = job.rate ? job.rate * 0.7 + rate * 0.3 : rate
          }
          if (ms < FAST_MS && job.piece < MAX_PIECE) job.piece = Math.min(MAX_PIECE, job.piece * 2)
          else if (ms > SLOW_MS && job.piece > MIN_PIECE) job.piece = Math.max(MIN_PIECE, job.piece / 2)
        } catch (e) {
          if (mine !== epoch) return
          it.inflight = 0
          const received = receivedOf(e)
          if (received !== undefined) {
            it.received = received // the server has another count: from there
            continue
          }
          if (aborted(e)) {
            if (it.state === 'sending') it.state = job.state === 'paused' ? 'paused' : 'waiting'
            break
          }
          const status = statusOf(e)
          if (status === 507) {
            job.full = true
            job.error = e
            this.pause(job.key)
            it.state = 'paused'
            break
          }
          if (status === 401) {
            // The session ended: the sign-in page is on its way.
            job.error = e
            this.pause(job.key)
            it.state = 'paused'
            break
          }
          const transient = status === 0 || status >= 500 || status === 429 || status === 408
          if (transient && tries < RETRY_DELAYS_MS.length) {
            // A smaller piece, in case this one was too long for the line
            // or a proxy's time; the server keeps what arrived of it, and
            // a 409 says from where to go on.
            job.piece = Math.max(MIN_PIECE, job.piece / 2)
            await sleep(RETRY_DELAYS_MS[tries++]!)
            if (mine !== epoch || it.state !== 'sending') break
            continue
          }
          it.state = 'error'
          it.error = e
          break
        } finally {
          controllers.delete(key)
        }
      }
      if (mine !== epoch) return
      if (!file && it.state === 'sending') it.state = 'missing'
      if (it.state === 'sending' && it.received >= it.size) it.state = 'sent'
      this.maybeComplete(job)
      this.pump()
    },

    /** Completes the upload once each file has arrived or was left out. */
    maybeComplete(job: UploadJob) {
      if (job.state !== 'sending' || !job.id) return
      if (job.items.every((it) => it.state === 'sent' || it.state === 'dropped')) void this.complete(job)
    },

    async complete(job: UploadJob) {
      const mine = epoch
      job.state = 'completing'
      job.serverError = undefined
      try {
        const drop = job.items.filter((it) => it.state === 'dropped').map((it) => it.n)
        await completeUpload(job.project, job.id, job.policy, drop)
      } catch (e) {
        if (mine !== epoch) return
        const missing = axios.isAxiosError(e) && e.response?.status === 409
          ? (e.response.data as { error?: { details?: { files?: unknown } } })?.error?.details?.files
          : undefined
        if (Array.isArray(missing) && missing.length) {
          // Files the server does not have whole: sent again.
          try {
            const u = await getUpload(job.project, job.id)
            for (const f of u.files ?? []) {
              const it = job.items.find((i) => i.n === f.n)
              if (it && missing.includes(f.n)) {
                it.received = f.received
                it.state = files.has(fileKey(job, f.n)) ? 'waiting' : 'missing'
              }
            }
          } catch {
            /* the sends say it */
          }
          job.state = 'sending'
          this.pump()
          return
        }
        if (axios.isAxiosError(e) && e.response?.status === 409) {
          // Completing or done already: follow it.
          void this.follow(job)
          return
        }
        job.state = 'error'
        job.error = e
        return
      }
      await this.follow(job)
    },

    /** Follows the server's completion to its results. */
    async follow(job: UploadJob) {
      const mine = epoch
      job.state = 'completing'
      for (let i = 0; ; i++) {
        await sleep(i < 10 ? POLL_MS : POLL_MS * 2)
        if (mine !== epoch || !this.jobs.includes(job)) return
        let u: UploadInfo
        try {
          u = await getUpload(job.project, job.id, false)
          if (u.state === 'done') u = await getUpload(job.project, job.id)
        } catch (e) {
          if (statusOf(e) === 404) {
            job.state = 'error'
            job.error = e
            return
          }
          continue
        }
        if (u.state === 'done') {
          for (const f of u.files ?? []) {
            const it = job.items.find((x) => x.n === f.n)
            if (it) it.result = f.result
          }
          job.state = 'done'
          files.forEach((_, k) => k.startsWith(job.key + '/') && files.delete(k))
          return
        }
        if (u.state === 'open') {
          job.state = 'error'
          job.serverError = u.error || 'stopped'
          return
        }
      }
    },

    /** Stops everything and forgets it: the sign-out. */
    reset() {
      epoch++
      controllers.forEach((c) => c.abort())
      controllers.clear()
      timers.forEach((t) => clearTimeout(t))
      timers.clear()
      files.clear()
      this.jobs = []
    },
  },
})

/** The time an upload still takes, in seconds, at its speed; 0 unknown. */
export function remainingSeconds(job: UploadJob): number {
  if (!job.rate) return 0
  let left = 0
  for (const it of job.items) if (it.state !== 'dropped' && it.state !== 'sent') left += it.size - it.received - it.inflight
  return Math.max(0, left / job.rate)
}

/** The bytes of an upload that arrived, its piece on the way included. */
export function sentBytes(job: UploadJob): number {
  return job.items.reduce((a, it) => a + (it.state === 'dropped' ? 0 : Math.min(it.size, it.received + it.inflight)), 0)
}

/** The bytes of an upload, its files left out aside. */
export function totalBytes(job: UploadJob): number {
  return job.items.reduce((a, it) => a + (it.state === 'dropped' ? 0 : it.size), 0)
}
