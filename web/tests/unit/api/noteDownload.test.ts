import { beforeEach, describe, expect, it, vi } from 'vitest'

const downloadFile = vi.fn()
const saveBlob = vi.fn()
const getNote = vi.fn()
vi.mock('@/api/download', () => ({ downloadFile: (...a: unknown[]) => downloadFile(...a), saveBlob: (...a: unknown[]) => saveBlob(...a) }))
vi.mock('@/api/notes', () => ({ getNote: (...a: unknown[]) => getNote(...a) }))

import { downloadNote } from '@/api/noteDownload'

describe('downloadNote', () => {
  beforeEach(() => {
    downloadFile.mockReset()
    saveBlob.mockReset()
    getNote.mockReset()
  })

  it('asks the server for the note with its attachments', async () => {
    downloadFile.mockResolvedValue(undefined)
    await downloadNote('Alpha/docs/a note.md')
    expect(downloadFile).toHaveBeenCalledWith('/notes/Alpha%2Fdocs%2Fa%20note.md?export=zip', 'a note.md')
    expect(saveBlob).not.toHaveBeenCalled()
  })

  it('saves the note in memory when the download fails', async () => {
    downloadFile.mockRejectedValue(new Error('offline'))
    await downloadNote('Alpha/a.md', { content: '# a', kind: 'note' } as never)
    expect(saveBlob).toHaveBeenCalledTimes(1)
    expect(saveBlob.mock.calls[0]![1]).toBe('a.md')
  })

  it('downloads a base as the file it is', async () => {
    await downloadNote('Alpha/books.base', { source: 'views: []', kind: 'base' } as never)
    expect(downloadFile).not.toHaveBeenCalled()
    expect(saveBlob.mock.calls[0]![1]).toBe('books.base')
  })
})
