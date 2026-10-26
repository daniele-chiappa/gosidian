import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { AxiosError, AxiosHeaders, type AxiosResponse } from 'axios'
import type { UploadFileInfo, UploadInfo } from '@/api/inUpload'

vi.mock('@/api/inUpload', () => ({
  openUpload: vi.fn(),
  getUpload: vi.fn(),
  putPiece: vi.fn(),
  completeUpload: vi.fn(),
  cancelUpload: vi.fn(),
  listUploads: vi.fn(),
}))

import { cancelUpload, completeUpload, getUpload, openUpload, putPiece } from '@/api/inUpload'
import { PARALLEL, pathProblem, useUploadsStore, type PickedFile } from '@/stores/uploads'
import { pickedFromInput, zipAdvised } from '@/composables/useUploadPick'

const file = (name: string, size: number) => new File([new Uint8Array(size)], name, { lastModified: 1700000000000 })

function info(files: Partial<UploadFileInfo>[], extra: Partial<UploadInfo> = {}): UploadInfo {
  return {
    id: 'up1', project: 'p', dir: '', policy: 'keep', owner: 'ann', channel: 'web', state: 'open',
    created: '', updated: '', expires: '', file_count: files.length, bytes: 0, received: 0,
    chunk_size: 8 << 20, max_chunk: 64 << 20,
    files: files.map((f, n) => ({ n, path: `f${n}`, size: 0, received: 0, ...f })),
    ...extra,
  }
}

function httpError(status: number, data: unknown = {}): AxiosError {
  const res = { status, data, statusText: '', headers: {}, config: { headers: new AxiosHeaders() } } as AxiosResponse
  return new AxiosError('failed', 'ERR_BAD_RESPONSE', res.config, null, res)
}

/** putPiece answering as a server would, from what it was sent. */
function serverPieces() {
  vi.mocked(putPiece).mockImplementation(async (_p, _id, n, offset, piece) => ({ n, received: offset + piece.size, size: 0 }))
}

describe('uploads store (IMP-172 phase 5)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    for (const f of [openUpload, getUpload, putPiece, completeUpload, cancelUpload]) vi.mocked(f).mockReset()
  })
  afterEach(() => vi.useRealTimers())

  it('sends a file in pieces from offset 0, completes it, follows it to the end', async () => {
    vi.useFakeTimers()
    const store = useUploadsStore()
    const picked: PickedFile[] = [{ file: file('a.bin', 20 << 20), path: 'a.bin' }]
    vi.mocked(openUpload).mockResolvedValue(info([{ path: 'a.bin', size: 20 << 20 }]))
    serverPieces()
    vi.mocked(completeUpload).mockResolvedValue(info([], { state: 'completing' }))
    vi.mocked(getUpload).mockResolvedValue(info([{ path: 'a.bin', size: 20 << 20, received: 20 << 20, result: { result: 'new', path: 'p/IN/a.bin' } }], { state: 'done' }))
    const job = (await store.add('p', '', picked))!
    expect(openUpload).toHaveBeenCalledWith('p', '', [{ path: 'a.bin', size: 20 << 20, unzip: undefined, modified: 1700000000000 }])
    await vi.waitFor(() => expect(completeUpload).toHaveBeenCalled())
    const offsets = vi.mocked(putPiece).mock.calls.map((c) => c[3])
    expect(offsets[0]).toBe(0)
    expect(offsets).toEqual([...offsets].sort((a, b) => a - b))
    expect(job.items[0]!.received).toBe(20 << 20)
    await vi.advanceTimersByTimeAsync(1100)
    expect(job.state).toBe('done')
    expect(job.items[0]!.result?.result).toBe('new')
  })

  it('goes on from the bytes the server has when it says another offset', async () => {
    const store = useUploadsStore()
    vi.mocked(openUpload).mockResolvedValue(info([{ path: 'a', size: 10 }]))
    vi.mocked(putPiece)
      .mockRejectedValueOnce(httpError(409, { error: { code: 'resource.conflict', details: { received: 6 } } }))
      .mockImplementation(async (_p, _id, n, offset, piece) => ({ n, received: offset + piece.size, size: 10 }))
    vi.mocked(completeUpload).mockReturnValue(new Promise(() => {}))
    await store.add('p', '', [{ file: file('a', 10), path: 'a' }])
    await vi.waitFor(() => expect(completeUpload).toHaveBeenCalled())
    expect(vi.mocked(putPiece).mock.calls[1]![3]).toBe(6)
    expect(vi.mocked(putPiece).mock.calls[1]![4].size).toBe(4)
  })

  it('tries again after a network error, with a smaller piece, from where the server is', async () => {
    vi.useFakeTimers()
    const store = useUploadsStore()
    vi.mocked(openUpload).mockResolvedValue(info([{ path: 'a', size: 10 }]))
    vi.mocked(putPiece)
      .mockRejectedValueOnce(new AxiosError('Network Error', 'ERR_NETWORK'))
      // The server kept 3 bytes of the piece the network lost.
      .mockRejectedValueOnce(httpError(409, { error: { details: { received: 3 } } }))
      .mockImplementation(async (_p, _id, n, offset, piece) => {
        pieceThen = store.jobs[0]!.piece
        return { n, received: offset + piece.size, size: 10 }
      })
    vi.mocked(completeUpload).mockReturnValue(new Promise(() => {}))
    let pieceThen = 0
    await store.add('p', '', [{ file: file('a', 10), path: 'a' }])
    await vi.advanceTimersByTimeAsync(1000)
    await vi.waitFor(() => expect(completeUpload).toHaveBeenCalled())
    expect(vi.mocked(putPiece).mock.calls[2]![3]).toBe(3)
    expect(pieceThen).toBe(4 << 20)
  })

  it('pauses on a full disk', async () => {
    const store = useUploadsStore()
    vi.mocked(openUpload).mockResolvedValue(info([{ path: 'a', size: 10 }]))
    vi.mocked(putPiece).mockRejectedValue(httpError(507, { error: { code: 'storage.full' } }))
    const job = (await store.add('p', '', [{ file: file('a', 10), path: 'a' }]))!
    await vi.waitFor(() => expect(job.state).toBe('paused'))
    expect(job.full).toBe(true)
    expect(job.items[0]!.state).toBe('paused')
  })

  it('asks for the files whose place is taken; skip leaves them out', async () => {
    const store = useUploadsStore()
    vi.mocked(openUpload).mockResolvedValue(info([
      { path: 'a', size: 1, exists: true },
      { path: 'b', size: 1 },
      { path: 'c.zip', size: 1, unzip: true, exists: true },
    ]))
    serverPieces()
    vi.mocked(completeUpload).mockReturnValue(new Promise(() => {}))
    const job = (await store.add('p', '', [
      { file: file('a', 1), path: 'a' }, { file: file('b', 1), path: 'b' }, { file: file('c.zip', 1), path: 'c.zip', unzip: true },
    ]))!
    expect(job.state).toBe('asking')
    expect(putPiece).not.toHaveBeenCalled()
    store.choose(job.key, 'skip')
    await vi.waitFor(() => expect(completeUpload).toHaveBeenCalled())
    expect(job.items.map((i) => i.state)).toEqual(['dropped', 'sent', 'sent'])
    expect(vi.mocked(completeUpload).mock.calls[0]).toEqual(['p', 'up1', 'skip', [0]])
  })

  it(`sends ${PARALLEL} files at a time`, async () => {
    const store = useUploadsStore()
    const n = PARALLEL + 2
    vi.mocked(openUpload).mockResolvedValue(info(Array.from({ length: n }, (_, i) => ({ path: `f${i}`, size: 5 }))))
    const pending: (() => void)[] = []
    vi.mocked(putPiece).mockImplementation(
      (_p, _id, k, offset, piece) => new Promise((resolve) => pending.push(() => resolve({ n: k, received: offset + piece.size, size: 5 }))),
    )
    vi.mocked(completeUpload).mockReturnValue(new Promise(() => {}))
    const job = (await store.add('p', '', Array.from({ length: n }, (_, i) => ({ file: file(`f${i}`, 5), path: `f${i}` }))))!
    expect(job.items.filter((i) => i.state === 'sending')).toHaveLength(PARALLEL)
    pending.shift()!()
    await vi.waitFor(() => expect(putPiece).toHaveBeenCalledTimes(PARALLEL + 1))
    expect(job.items.filter((i) => i.state === 'sending')).toHaveLength(PARALLEL)
  })

  it('pauses and resumes', async () => {
    const store = useUploadsStore()
    vi.mocked(openUpload).mockResolvedValue(info([{ path: 'a', size: 4 }]))
    vi.mocked(putPiece).mockImplementationOnce((_p, _id, _n, _o, _b, opts) => new Promise((_, reject) => {
      opts?.signal?.addEventListener('abort', () => reject(new AxiosError('canceled', AxiosError.ERR_CANCELED)))
    }))
    const job = (await store.add('p', '', [{ file: file('a', 4), path: 'a' }]))!
    store.pause(job.key)
    await vi.waitFor(() => expect(job.items[0]!.state).toBe('paused'))
    serverPieces()
    vi.mocked(completeUpload).mockReturnValue(new Promise(() => {}))
    store.resume(job.key)
    await vi.waitFor(() => expect(completeUpload).toHaveBeenCalled())
    expect(job.items[0]!.state).toBe('sent')
  })

  it('resumes an upload of the server with the files chosen again', async () => {
    const store = useUploadsStore()
    vi.mocked(getUpload).mockResolvedValue(info([{ path: 'd/a', size: 6, received: 4 }, { path: 'd/b', size: 2 }, { path: 'd/c', size: 3, received: 3 }]))
    serverPieces()
    vi.mocked(completeUpload).mockReturnValue(new Promise(() => {}))
    const job = await store.resumeWith('p', 'up1', [{ file: file('a', 6), path: 'd/a' }])
    await vi.waitFor(() => expect(putPiece).toHaveBeenCalled())
    expect(vi.mocked(putPiece).mock.calls[0]![3]).toBe(4)
    await vi.waitFor(() => expect(job.items[0]!.state).toBe('sent'))
    expect(job.items.map((i) => i.state)).toEqual(['sent', 'missing', 'sent'])
    expect(completeUpload).not.toHaveBeenCalled()
  })

  it('drops everything at the sign-out', async () => {
    const store = useUploadsStore()
    vi.mocked(openUpload).mockResolvedValue(info([{ path: 'a', size: 4 }]))
    vi.mocked(putPiece).mockReturnValue(new Promise(() => {}))
    await store.add('p', '', [{ file: file('a', 4), path: 'a' }])
    store.reset()
    expect(store.jobs).toEqual([])
    expect(store.busy).toBe(false)
  })
})

describe('the files chosen', () => {
  it('leaves out what IN refuses', () => {
    expect(pathProblem('a/b.txt')).toBe('')
    expect(pathProblem('a/.DS_Store')).toBe('hidden')
    expect(pathProblem('.git/config')).toBe('hidden')
    for (const p of ['a//b', ' a', 'a ', 'a\\b', 'a\u0001', 'n'.repeat(256)]) expect(pathProblem(p)).toBe('name')
  })

  it('keeps a folder path, NFC, and counts the hidden files', () => {
    const f = (name: string, rel: string) => {
      const x = file(name, 1)
      Object.defineProperty(x, 'webkitRelativePath', { value: rel })
      return x
    }
    const p = pickedFromInput([f('a.txt', 'top/a.txt'), f('.DS_Store', 'top/.DS_Store'), f('café', 'top/café')], 'folder')
    expect(p.files.map((x) => x.path)).toEqual(['top/a.txt', 'top/café'])
    expect(p.hidden).toBe(1)
    const z = pickedFromInput([file('x.zip', 1), file('y.txt', 1)], 'zip')
    expect(z.files).toEqual([expect.objectContaining({ path: 'x.zip', unzip: true })])
  })

  it('advises a zip for many small files', () => {
    expect(zipAdvised(Array.from({ length: 600 }, (_, i) => ({ file: file(`${i}`, 10), path: `${i}` })))).toBe(true)
    expect(zipAdvised([{ file: file('a', 10), path: 'a' }])).toBe(false)
  })
})
