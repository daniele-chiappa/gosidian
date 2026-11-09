import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import type { ViewData } from '@/api/preview'

vi.mock('@/api/notes', () => ({
  patchFrontmatter: vi.fn(),
  getNewRow: vi.fn().mockResolvedValue({ name: '', source: 'p', columns: [] }),
  createRow: vi.fn(),
  createNote: vi.fn(),
}))
vi.mock('@/composables/useAnnounce', () => ({ announce: vi.fn() }))
import { patchFrontmatter } from '@/api/notes'
import { announce } from '@/composables/useAnnounce'
import ViewBoard from '@/components/views/ViewBoard.vue'
import NewRowForm from '@/components/views/NewRowForm.vue'

const patch = vi.mocked(patchFrontmatter)

function board(over: Partial<ViewData> = {}): ViewData {
  return {
    as: 'board',
    total: 3,
    group: { name: 'status', type: 'select', options: ['todo', 'doing', 'done'] },
    groups: ['todo', 'doing', 'done'],
    columns: [{ name: 'title' }, { name: 'priority', type: 'select' }],
    rows: [
      {
        path: 'p/T-1.md',
        title: 'One',
        modified: '',
        fields: { status: 'todo', priority: 'high' },
        writable: true,
      },
      { path: 'p/T-2.md', title: 'Two', modified: '', fields: { status: 'todo' }, writable: false },
      {
        path: 'p/T-3.md',
        title: 'Three',
        modified: '',
        fields: { status: 'done' },
        writable: true,
      },
    ],
    ...over,
  }
}

function mountBoard(v = board(), attachTo?: HTMLElement) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(ViewBoard, {
    props: { view: v },
    global: { plugins: [i18n], provide: { openWindow: vi.fn() } },
    attachTo,
  })
}

const column = (w: ReturnType<typeof mountBoard>, value: string) =>
  w.find(`[data-group="${value}"]`)

async function moveWithMenu(w: ReturnType<typeof mountBoard>, path: string, to: string) {
  await w.find(`li[data-card="${path}"] button`).trigger('click')
  await w.find(`li[data-card="${path}"] [data-move-to="${to}"]`).trigger('click')
  await flushPromises()
}

describe('ViewBoard', () => {
  beforeEach(() => {
    patch.mockReset()
  })

  it('shows a column per value, empty ones included, with the cards in them', () => {
    const w = mountBoard()
    expect(w.findAll('[data-group]').map((s) => s.attributes('data-group'))).toEqual([
      'todo',
      'doing',
      'done',
    ])
    expect(column(w, 'todo').findAll('li[data-card]')).toHaveLength(2)
    expect(column(w, 'doing').text()).toContain('No notes')
    // A card shows its fields but the group_by one.
    expect(w.find('li[data-card="p/T-1.md"]').text()).toContain('Priority:')
    expect(w.find('li[data-card="p/T-1.md"]').text()).not.toContain('Status:')
  })

  it('draws the columns as the items of a list, not as regions (IMP-217, F-35)', () => {
    const w = mountBoard()
    const items = w.findAll('[role="list"] > [role="listitem"]')
    expect(items.map((c) => c.attributes('aria-label'))).toEqual(['todo', 'doing', 'done'])
    expect(w.find('section').exists()).toBe(false)
  })

  it('moves a card with its menu, the column it was in as expect', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard()
    await moveWithMenu(w, 'p/T-1.md', 'doing')
    expect(patch).toHaveBeenCalledWith('p/T-1.md', {
      set: { status: 'doing' },
      expect: { status: 'todo' },
    })
    expect(column(w, 'doing').find('li[data-card="p/T-1.md"]').exists()).toBe(true)
  })

  it('says to a screen reader where a card went on a board of a view too (IMP-217, F-10)', async () => {
    patch.mockResolvedValue({} as never)
    vi.mocked(announce).mockClear()
    const w = mountBoard()
    await moveWithMenu(w, 'p/T-1.md', 'doing')
    expect(announce).toHaveBeenLastCalledWith('Moved to doing, place 1')
  })

  it('writes a bool for a checkbox column, and removes the field in the no-value column', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard(
      board({
        group: { name: 'done', type: 'checkbox' },
        groups: ['false', 'true', ''],
        rows: [
          {
            path: 'p/T-1.md',
            title: 'One',
            modified: '',
            fields: { done: 'false' },
            writable: true,
          },
        ],
      }),
    )
    await moveWithMenu(w, 'p/T-1.md', 'true')
    expect(patch).toHaveBeenLastCalledWith('p/T-1.md', {
      set: { done: true },
      expect: { done: 'false' },
    })
    await moveWithMenu(w, 'p/T-1.md', '')
    expect(patch).toHaveBeenLastCalledWith('p/T-1.md', {
      unset: ['done'],
      expect: { done: 'true' },
    })
  })

  it('puts a card back where it now is when it was moved meanwhile', async () => {
    const conflict = new AxiosError('failed', '409', undefined, undefined, {
      status: 409,
      statusText: '',
      headers: {},
      config: { headers: new AxiosHeaders() },
      data: { error: { details: { fields: { status: 'done' } } } },
    })
    patch.mockImplementation(async () => {
      throw conflict
    })
    const w = mountBoard()
    await moveWithMenu(w, 'p/T-1.md', 'doing')
    expect(column(w, 'done').find('li[data-card="p/T-1.md"]').exists()).toBe(true)
    expect(w.find('[data-view-message]').text()).toContain('changed meanwhile')
  })

  it('does not move a card the reader may not write', () => {
    const w = mountBoard()
    const card = w.find('li[data-card="p/T-2.md"]')
    expect(card.attributes('draggable')).toBe('false')
    expect(card.find('button').exists()).toBe(false)
  })

  it("opens a new row's form in a column, with that column's value", async () => {
    const w = mountBoard(
      board({
        creatable: true,
        database: 'p/tasks.md',
        defaults: { priority: 'high', status: 'todo' },
      }),
    )
    expect(
      w.find('[data-group="doing"] [data-new-row-button]').attributes('aria-label'),
    ).toBe('New row in doing')
    await w.find('[data-group="doing"] [data-new-row-button]').trigger('click')
    const form = w.findComponent(NewRowForm)
    expect(form.props('values')).toEqual({ priority: 'high', status: 'doing' })
    expect(w.find('[data-group="doing"] [data-new-row]').exists()).toBe(true)
    form.vm.$emit('done')
    await flushPromises()
    expect(w.findComponent(NewRowForm).exists()).toBe(false)
  })

  it('offers no new row to a reader who may not add one', () => {
    expect(mountBoard().find('[data-new-row-button]').exists()).toBe(false)
  })
})

// The board of a ```board block (IMP-216): its columns are the block's,
// with their limits, and a card moves to a place in a column.
function cards(over: Partial<ViewData> = {}): ViewData {
  return {
    as: 'board',
    total: 4,
    group: { name: 'column', type: 'text' },
    groups: ['Idee', 'Pronto', 'Analisi', ''],
    lanes: [
      { name: 'Idee', status: 'backlog' },
      { name: 'Pronto', status: 'submitted' },
      { name: 'Analisi', status: 'working', wip: 1 },
    ],
    columns: [{ name: 'title' }, { name: 'claimed_by', type: 'text' }],
    rows: [
      {
        path: 'p/tasks/a.md',
        title: 'A',
        modified: '',
        fields: { column: 'Pronto', rank: '1' },
        writable: true,
      },
      {
        path: 'p/tasks/b.md',
        title: 'B',
        modified: '',
        fields: { column: 'Pronto', rank: '2' },
        writable: true,
      },
      {
        path: 'p/tasks/c.md',
        title: 'C',
        modified: '',
        fields: { column: 'Analisi', rank: '1' },
        writable: true,
      },
      { path: 'p/tasks/d.md', title: 'D', modified: '', fields: {}, writable: true },
    ],
    database: 'p/tasks.md',
    creatable: true,
    defaults: { board: '[[p/board]]' },
    ...over,
  }
}

const titles = (w: ReturnType<typeof mountBoard>, value: string) =>
  column(w, value)
    .findAll('li[data-card]')
    .map((li) => li.find('a').text())

describe("ViewBoard in the reader's language (IMP-220, F-23)", () => {
  it('names its columns by the values and its cards\' fields by their names', () => {
    const i18n = createI18n({ legacy: false, locale: 'it', fallbackLocale: 'en', messages: { en: enUI, it: itUI } })
    const v = board({
      group: { name: 'status', type: 'select', options: ['submitted', 'working'] },
      groups: ['submitted', 'working', ''],
      rows: [{ path: 'p/T-1.md', title: 'One', modified: '', fields: { status: 'submitted', priority: 'high' }, writable: true }],
    })
    v.columns = [{ name: 'title' }, { name: 'priority', type: 'select', options: ['low', 'high'] }]
    const w = mount(ViewBoard, { props: { view: v }, global: { plugins: [i18n], provide: { openWindow: vi.fn() } } })
    const lanes = w.findAll('[role="list"] > [role="listitem"]')
    expect(lanes.map((c) => c.attributes('aria-label'))).toEqual(['inviato', 'in lavorazione', 'vuoto'])
    // The column's value stays the code: it is what a move writes.
    expect(lanes[0]!.attributes('data-group')).toBe('submitted')
    const card = w.find('li[data-card="p/T-1.md"]')
    expect(card.find('dt').text()).toBe('Priorità:')
    expect(card.find('dd').text()).toBe('alta')
  })
})

describe('ViewBoard of a board block', () => {
  beforeEach(() => {
    patch.mockReset()
  })

  it('shows its columns with their limits, the closed cards on request', async () => {
    const w = mountBoard(cards())
    expect(w.findAll('[data-group]').map((s) => s.attributes('data-group'))).toEqual([
      'Idee',
      'Pronto',
      'Analisi',
    ])
    expect(column(w, 'Analisi').find('[data-wip]').text()).toBe('1/1')
    expect(column(w, 'Analisi').find('[data-wip]').classes()).not.toContain('text-danger')
    expect(column(w, 'Pronto').find('[data-wip]').text()).toBe('2')
    await w.find('[data-show-closed]').trigger('click')
    expect(column(w, '').text()).toContain('Closed')
    expect(titles(w, '')).toEqual(['D'])
  })

  it('shows a column past its limit in red', () => {
    const v = cards()
    v.rows![1]!.fields.column = 'Analisi'
    const w = mountBoard(v)
    expect(column(w, 'Analisi').find('[data-wip]').text()).toBe('2/1')
    expect(column(w, 'Analisi').find('[data-wip]').classes()).toContain('text-danger')
  })

  it('moves a card to a column, and says when it went past the limit', async () => {
    patch.mockResolvedValue({
      warning: 'the column Analisi of p/board.md holds 1 cards, its wip',
    } as never)
    const w = mountBoard(cards())
    await moveWithMenu(w, 'p/tasks/a.md', 'Analisi')
    expect(patch).toHaveBeenCalledWith('p/tasks/a.md', {
      set: { column: 'Analisi' },
      expect: { column: 'Pronto' },
    })
    expect(titles(w, 'Analisi')).toEqual(['C', 'A'])
    expect(w.find('[data-view-message]').text()).toContain('Analisi: past its limit of 1')
  })

  it('moves a card up its column when dropped on another', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard(cards())
    await w.find('li[data-card="p/tasks/b.md"]').trigger('dragstart')
    await w.find('li[data-card="p/tasks/a.md"]').trigger('dragover')
    await w.find('li[data-card="p/tasks/a.md"]').trigger('drop')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/tasks/b.md', { at: 0, expect: { column: 'Pronto' } })
    expect(titles(w, 'Pronto')).toEqual(['B', 'A'])
  })

  it('drops a card of another column at the place of the one it falls on', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard(cards())
    await w.find('li[data-card="p/tasks/c.md"]').trigger('dragstart')
    await w.find('li[data-card="p/tasks/b.md"]').trigger('drop')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/tasks/c.md', {
      set: { column: 'Pronto' },
      at: 1,
      expect: { column: 'Analisi' },
    })
    expect(titles(w, 'Pronto')).toEqual(['A', 'C', 'B'])
  })

  it('moves a card with Alt and the arrows, the way for the keyboard', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard(cards())
    await w
      .find('li[data-card="p/tasks/b.md"]')
      .trigger('keydown', { key: 'ArrowUp', altKey: true })
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/tasks/b.md', { at: 0, expect: { column: 'Pronto' } })
    expect(titles(w, 'Pronto')).toEqual(['B', 'A'])
    await w
      .find('li[data-card="p/tasks/b.md"]')
      .trigger('keydown', { key: 'ArrowUp', altKey: true })
    expect(patch).toHaveBeenCalledTimes(1) // already at the top
  })

  it('says to a screen reader where a card went, and nothing when the move is refused (IMP-217, F-10)', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard(cards())
    await w
      .find('li[data-card="p/tasks/b.md"]')
      .trigger('keydown', { key: 'ArrowUp', altKey: true })
    await flushPromises()
    expect(announce).toHaveBeenLastCalledWith('Moved to Pronto, place 1')
    await moveWithMenu(w, 'p/tasks/a.md', 'Analisi')
    expect(announce).toHaveBeenLastCalledWith('Moved to Analisi, place 2')

    vi.mocked(announce).mockClear()
    patch.mockRejectedValue(new AxiosError('failed', '500'))
    await moveWithMenu(w, 'p/tasks/b.md', 'Analisi')
    expect(announce).not.toHaveBeenCalled()
  })

  it('puts the card back in its place when the move is refused', async () => {
    const refused = new AxiosError('failed', '422', undefined, undefined, {
      status: 422,
      statusText: '',
      headers: {},
      config: { headers: new AxiosHeaders() },
      data: {
        error: {
          message:
            'the column Analisi of p/board.md holds 1 cards, its wip: finish or move one of them first',
        },
      },
    })
    patch.mockImplementation(async () => {
      throw refused
    })
    const w = mountBoard(cards())
    await w.find('li[data-card="p/tasks/b.md"]').trigger('dragstart')
    await w.find('li[data-card="p/tasks/c.md"]').trigger('drop')
    await flushPromises()
    expect(titles(w, 'Pronto')).toEqual(['A', 'B'])
    expect(titles(w, 'Analisi')).toEqual(['C'])
    expect(w.find('[data-view-message]').text()).toContain('finish or move one of them first')
  })

  it('makes a new card in a column of backlog or submitted, at its bottom', async () => {
    const w = mountBoard(cards())
    expect(column(w, 'Idee').find('[data-new-row-button]').exists()).toBe(true)
    expect(column(w, 'Analisi').find('[data-new-row-button]').exists()).toBe(false)
    await column(w, 'Pronto').find('[data-new-row-button]').trigger('click')
    expect(w.findComponent(NewRowForm).props('values')).toEqual({
      board: '[[p/board]]',
      column: 'Pronto',
      status: 'submitted',
      rank: 3,
    })
  })
})

describe('ViewBoard, the focus on the card moved (IMP-217, F-04)', () => {
  beforeEach(() => {
    patch.mockReset().mockResolvedValue({} as never)
  })

  const moveButton = (w: ReturnType<typeof mountBoard>, path: string) =>
    w.find(`li[data-card="${path}"] button`).element

  it('puts it on the card in its new column, after the menu', async () => {
    const w = mountBoard(cards(), document.body)
    await moveWithMenu(w, 'p/tasks/a.md', 'Analisi')
    expect(titles(w, 'Analisi')).toEqual(['C', 'A'])
    expect(document.activeElement).toBe(moveButton(w, 'p/tasks/a.md'))
    w.unmount()
  })

  it('keeps it on the card moved with Alt and the arrows', async () => {
    const w = mountBoard(cards(), document.body)
    const link = w.find('li[data-card="p/tasks/b.md"] a').element as HTMLAnchorElement
    link.focus()
    // Moved in the page, the focused card lost the focus.
    link.blur()
    await w.find('li[data-card="p/tasks/b.md"]').trigger('keydown', { key: 'ArrowUp', altKey: true })
    await flushPromises()
    expect(document.activeElement).toBe(moveButton(w, 'p/tasks/b.md'))
    w.unmount()
  })

  it('puts it on the card put back when the move is refused', async () => {
    patch.mockRejectedValue(new AxiosError('failed', '500'))
    const w = mountBoard(cards(), document.body)
    await moveWithMenu(w, 'p/tasks/a.md', 'Analisi')
    expect(titles(w, 'Pronto')).toEqual(['A', 'B'])
    expect(document.activeElement).toBe(moveButton(w, 'p/tasks/a.md'))
    w.unmount()
  })

  it('does the same on the board of a view', async () => {
    const w = mountBoard(board(), document.body)
    await moveWithMenu(w, 'p/T-1.md', 'doing')
    expect(document.activeElement).toBe(w.find('[data-group="doing"] li[data-card="p/T-1.md"] button').element)
    w.unmount()
  })
})

describe('ViewBoard, the menu of "Move to…" (IMP-216, F-12)', () => {
  beforeEach(() => {
    patch.mockReset().mockResolvedValue({} as never)
  })

  it('opens on the card\'s column; the arrows choose without moving, Esc closes', async () => {
    const w = mountBoard(cards(), document.body)
    await w.find('li[data-card="p/tasks/a.md"] button').trigger('click')
    const menu = w.find('li[data-card="p/tasks/a.md"] [data-move-menu]')
    expect(menu.attributes('role')).toBe('menu')
    expect(document.activeElement).toBe(menu.find('[data-move-to="Pronto"]').element)
    await menu.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(menu.find('[data-move-to="Analisi"]').element)
    await menu.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(menu.find('[data-move-to="Idee"]').element)
    await menu.trigger('keydown', { key: 'ArrowUp' })
    expect(patch).not.toHaveBeenCalled()
    await menu.trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(w.find('[data-move-menu]').exists()).toBe(false)
    expect(patch).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(w.find('li[data-card="p/tasks/a.md"] button').element)
    w.unmount()
  })

  it('moves the card with the item chosen', async () => {
    const w = mountBoard(cards(), document.body)
    await moveWithMenu(w, 'p/tasks/a.md', 'Idee')
    expect(patch).toHaveBeenCalledWith('p/tasks/a.md', { set: { column: 'Idee' }, expect: { column: 'Pronto' } })
    expect(titles(w, 'Idee')).toEqual(['A'])
    w.unmount()
  })
})

// The state of a board (IMP-216 phase 2): its head, its stale cards, the
// cards of its iteration.
function withState(): ViewData {
  const v = cards()
  v.rows![0]!.fields.iteration = '[[p/iterations/1]]'
  v.rows![2]!.fields.iteration = '[[p/iterations/1]]'
  v.board = {
    flow: {
      cycle_days: { p50: 2.5, p85: 4, n: 5 },
      closed_per_week: [3, 5, 2, 4],
      stale: [{ path: 'p/tasks/c.md', title: 'C', column: 'Analisi', days: 5 }],
    },
    stale_days: 3,
    iteration: {
      path: 'p/iterations/1.md',
      title: 'Iterazione 1',
      board: 'p/board.md',
      goal: 'Il login',
      start: '2026-11-09',
      end: '2026-11-22',
      days_left: 4,
      cards: { done: 1, total: 2 },
    },
    retro: [{ path: 'p/iterations/0.md', title: 'Iterazione 0', board: 'p/board.md', start: '2026-10-26', end: '2026-11-08' }],
  }
  return v
}

// The board's catalogue is a chunk of its own, loaded at the first board.
async function headReady(w: ReturnType<typeof mountBoard>) {
  await vi.waitFor(() => expect(w.find('[data-board-head]').exists()).toBe(true))
}

describe('ViewBoard, the head and the stale cards (IMP-216 phase 2)', () => {
  it('shows the iteration, the retrospective to write and the three measures', async () => {
    const w = mountBoard(withState())
    await headReady(w)
    const head = w.find('[data-board-head]')
    expect(head.find('[data-iteration]').text()).toContain('Il login')
    expect(head.find('[data-iteration]').text()).toContain('4 days left')
    expect(head.find('[data-iteration]').text()).toContain('1 of 2 done')
    expect(head.find('[data-retro]').text()).toContain('Iterazione 0')
    expect(head.find('[data-cycle]').text()).toBe('Cycle time: 2.5 d (85%: 4 d)')
    expect(head.find('[data-closed]').text()).toBe('Closed: 3 · 5 · 2 · 4 a week')
    expect(head.find('[data-stale-count]').text()).toBe('Stale: 1')
  })

  it('marks a stale card, and says it in its name', async () => {
    const w = mountBoard(withState())
    await headReady(w)
    const card = w.find('li[data-card="p/tasks/c.md"]')
    expect(card.find('[data-stale]').text()).toBe('stale for 5 d')
    expect(card.find('[data-stale]').attributes('aria-hidden')).toBe('true')
    expect(card.find('a').text()).toContain('stale for 5 days')
    expect(w.find('li[data-card="p/tasks/a.md"] [data-stale]').exists()).toBe(false)
  })

  it('leaves only the cards of the iteration on request', async () => {
    const w = mountBoard(withState())
    await headReady(w)
    await w.find('[data-only-iteration]').trigger('click')
    expect(w.find('[data-only-iteration]').attributes('aria-pressed')).toBe('true')
    expect(titles(w, 'Pronto')).toEqual(['A'])
    expect(titles(w, 'Analisi')).toEqual(['C, stale for 5 days'])
    // The limits count every card.
    expect(column(w, 'Pronto').find('[data-wip]').text()).toBe('2')
    await w.find('[data-only-iteration]').trigger('click')
    expect(titles(w, 'Pronto')).toEqual(['A', 'B'])
  })

  it('makes a new iteration of the board when none is in progress', async () => {
    const { createNote } = await import('@/api/notes')
    vi.mocked(createNote).mockResolvedValue({ path: 'p/iterations/sprint-1.md' } as never)
    const v = withState()
    delete v.board!.iteration
    const w = mountBoard(v)
    await headReady(w)
    await w.find('[data-new-iteration]').trigger('click')
    const form = w.find('[data-iteration-form]')
    await form.find('input[type="text"]').setValue('Sprint 1')
    await form.find('[data-goal]').setValue('Il login: con le passkey')
    await form.trigger('submit')
    await flushPromises()
    const [path, content] = vi.mocked(createNote).mock.calls[0]!
    expect(path).toBe('p/iterations/sprint-1.md')
    expect(content).toContain('type: iteration\nboard: "[[p/board]]"\ngoal: "Il login: con le passkey"\nstart: ')
    expect(w.find('[data-iteration-form]').exists()).toBe(false)
  })

  it('shows no head on the board of a view', async () => {
    const w = mountBoard(board())
    await flushPromises()
    expect(w.find('[data-board-head]').exists()).toBe(false)
  })
})

describe('ViewBoard, the columns out of view (IMP-216, F-28)', () => {
  it('shows a shadow and an arrow at the edge that hides some, and how many show', async () => {
    const w = mountBoard(cards(), document.body)
    await flushPromises()
    expect(w.find('[data-columns-shown]').exists()).toBe(false)
    const el = w.find('[role="list"]').element as HTMLElement
    Object.defineProperty(el, 'scrollWidth', { configurable: true, value: 720 })
    Object.defineProperty(el, 'clientWidth', { configurable: true, value: 480 })
    const lanes = [...el.children] as HTMLElement[]
    lanes.forEach((lane, i) => {
      Object.defineProperty(lane, 'offsetLeft', { configurable: true, value: i * 240 })
      Object.defineProperty(lane, 'offsetWidth', { configurable: true, value: 224 })
    })
    await w.find('[role="list"]').trigger('scroll')
    await vi.waitFor(() => expect(w.find('[data-columns-shown]').text()).toBe('2 of 3 columns'))
    expect(w.find('[data-scroll-next]').attributes('aria-label')).toBe('Next column')
    expect(w.find('[data-scroll-prev]').exists()).toBe(false)
    w.unmount()
  })
})
