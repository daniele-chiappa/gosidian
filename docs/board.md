# Boards

A board is the work of a project in columns: each card a task, each
column a state of the tasks with a limit of cards at work, and the order
of the cards in a column what comes first (IMP-216). An agent in a
session takes the board's next card, moves it and closes it with the task
tools; a person drags the cards in the web UI. Both go through the same
rules: the limits, the order, the columns of each state.

- **A board** is a note `type: board` with one ```` ```board ```` block:
  its columns, how hard their limits are, who closes a card. The rest of
  the note says, in its own words, what done means on this board and how
  the work goes: every agent that takes a card reads it.
- **A card** is a task of the project's `tasks/` folder, a row of the
  database `tasks.md`, with `board`, `column` and `rank`. It has no team:
  the [teams of agents](agents.md) work on their own tasks, and take no
  card yet.

There is no default board: write the note, as below, where the project
wants it.

## A board

````markdown
---
title: Board
type: board
tags: [myproject]
---

# Board

```board
columns:
  - {name: Ideas, status: backlog}
  - {name: Ready, status: submitted}
  - {name: Analysis, status: working, wip: 1}
  - {name: Development, status: working, wip: 2}
  - {name: Waiting, status: input-required}
  - {name: Review, status: review, wip: 3}
  - {name: Done, status: completed}
wip: {agents: hard, people: warn}
done: agent
```

## Done

A card is done when its tests pass, the CHANGELOG says what changed, and
the plan it serves records it.

## How we work

Take the next card of Ready. Say in the card what is left when you stop.
````

| Field | |
|---|---|
| `columns` | the columns in their order, at most 20: `name` (at most 64 bytes, one of a kind in any case), `status` (`backlog`, `submitted`, `working`, `input-required`, `review`, `completed`, `failed`, `canceled`) and `wip`, the most cards the column holds (a whole number from 1; none: no limit) |
| `wip` | how hard the limits are: for `agents` (default `hard`: a card past a column's limit is refused) and for `people` (default `warn`: the card goes, and the board says the column is past its limit) |
| `done` | who closes a card: `agent` (default), or `person`: an agent's completion puts the card in review with its report, and a person closes it. A card's own `done_by` wins |

A board has a column of `submitted`, where an agent takes the next card,
and a column of `working`, where the card goes; with `done: person`, a
column of `review` too. Several columns may share a state, as Analysis
and Development above.

**Columns and states.** A card's column and its `status` agree. A card
moved to a column takes the column's state; a card moved to a state stays
in its column when that column is of the state, and otherwise goes to the
first column of the state. A closed card (`completed`, `failed`,
`canceled`) whose state has no column has none: the board shows it among
the closed. A card written by hand whose column disagrees with its state
is reported by the lint, and its next move gives it a column of its
state.

**The order.** `rank` orders a column's cards, the smaller first. The
server keeps it: a card put between two others takes the rank halfway
between theirs, and the column is renumbered 1, 2, 3… when the room
between two runs out or a card has no rank.

**The limits.** A card that enters a full column, as an agent's claim, a
move or a completion, is refused when the board is `hard` for who moves
it, and goes with a warning when it is `warn`. A card moved within its
own column is never refused. The count and the write happen under the
board's lock: two agents that ask for the last place at once do not both
get it.

## A card

```yaml
---
title: The login page
type: task
status: submitted
board: "[[myproject/board]]"
column: Ready
rank: 2
column_since: 2026-10-31T10:12:00+01:00
for: ["[[myproject/docs/improvements/IMP-012]]"]
depends_on: ["[[myproject/tasks/20261030-151200-the-users-table]]"]
tags: [myproject, type:task]
---

# The login page

What to do, and how it is accepted.
```

| Field | |
|---|---|
| `board` | the board's note |
| `column` | its column on the board |
| `rank` | its place in the column, kept by the server |
| `column_since` | when it entered its column |
| `done_by` | `agent` or `person`; default the board's `done` |
| `for` | the notes it serves: an improvement, a plan, a bug. A [row view](vault/databases.md#row-views) of their database lists their cards (below) |

A card is a task, with the task's other fields: `depends_on` (a card
starts when they are completed), `claimed_by`, `lease_until` and
`attempts` (kept by the server), `paths`, `forbid`, `budget_usd`. A card
has no `team` or `assignee`: the teams take no card yet, and
`memory_task_create` refuses a card with them.

The first card of a project rewrites the `fields` of its `tasks.md`, as
the server: `backlog` goes first among the options of `status`, `team`
and `assignee` are no longer required, and the card's fields join the
schema; a project without a `tasks.md` gets one with them. The rest of
the note stays as written.

## Agents

An agent in a session works on the cards with its MCP token, as an
[outside agent](agents.md#outside-agents) works on a team's tasks:

- `memory_task_create(project, board, title, column?, for?, done_by?,
  depends_on?, body?)` writes a card at the bottom of its column, of
  `backlog` or `submitted` (default the board's first);
- `memory_task_claim(board)` takes the board's next card: in the columns
  of `submitted`, in their order, the first by rank whose dependencies are
  completed and that nobody holds. `memory_task_claim(path)` takes that
  card. Either puts it in the first column of `working`, with a lease
  that `memory_task_heartbeat` renews. A card held past its lease goes
  back to the top of the first column of `submitted` with the next move on
  its board, an agent's or a person's, before its columns are counted; at
  the `[agents] max_attempts`-th attempt (3) it fails;
- `memory_task_update(path, column)` moves the card it holds to a column
  of `working`, `input-required`, `review` or `submitted` (which gives it
  back), with a `message` in the card;
- `memory_task_complete(path, report)` closes it with its report, or puts
  it in review for a person when the card is done by a person;
- `memory_tasks(project, board)` lists the board's cards, and its
  columns with the cards of each and their limit.

The answer of each tool on a card holds the board's text (`board.text`,
for a token that reads): its note without the frontmatter and the board
and view blocks, the first 8 KB of it, and a `warning` when the card took
a column past its limit. The agent of a team's run creates and takes no card.

`memory_get` of the board's note with `render_views: true` shows the
cards under the block, one list for each column that has some, in the
board's order, with its count and limit:

```text
**Ready** (2)

- [[myproject/tasks/20261031-101200-the-login-page\|The login page]] · for: [[myproject/docs/improvements/IMP-012]]
- [[myproject/tasks/20261031-101500-the-logout\|The logout]]

**Analysis** (1, wip 1)

- [[myproject/tasks/20261030-151200-the-users-table\|The users table]] · claimed_by: claude@laptop

**(closed)** (1)

- [[myproject/tasks/20261029-090000-the-old-form\|The old form]]
```

## The web UI

The board's note shows the board in place of its block: a column for
each of the block's, empty ones too, with its count against its limit
(`1/1`), red past it. The closed cards without a column wait behind a
"Closed (n)" button.

- Dragging a card onto another puts it in that one's place, in its own
  column or another; dropping it below the cards puts it at the bottom.
  Alt with the arrow keys moves the card focused up and down, and its
  "Move to…" menu sends it to another column. The card keeps the focus,
  in its new column, and a screen reader hears where it went: "Moved to
  Analysis, place 2".
- A person moves a card to any column, by the board's limits for people.
  Moved back to `backlog` or `submitted`, the card lets its holder go;
  closed, it ends the hold. A refused move puts the card back with the
  reason; a column past its limit says so ("Analysis: past its limit of
  1").
- The "+" of a column of `backlog` or `submitted` writes a new card there,
  at its bottom.

`PATCH /api/v1/notes/<card>/frontmatter` moves a card when it touches its
place, as the web UI does:

```json
{"set": {"column": "Analysis"}, "at": 0, "expect": {"column": "Ready"}}
```

- `set` gives `column` or `status` (or both, when they agree), and `at`
  the card's place in its column from 0, the top: with a new column, where
  it enters (default its bottom); alone, where it goes in its own.
- A move changes nothing else: another key with it, `rank` (the server
  keeps it) or an `unset` of these keys answers **400**.
- `expect` and `if_match` work as for any field (**409**, **412**). A move
  the board refuses (a column it has not, a full column of a `hard`
  board) answers **422** with the reason. A move answers 200 with the
  note and, when the column went past its limit, `warning`.

The move is written as the person, in the audit log, as any edit of the
web UI. Fields of a card other than its place are edited as any row's.

## The notes a card serves

A database whose rows the cards serve shows them under each row with a
row view on `for`:

```yaml
row_views:
  - title: Cards
    from: myproject/tasks
    where: [for contains this]
    columns: [title, status, column]
```

## Lint

`memory_lint`'s rule `board-invalid` reports a ```` ```board ```` block
that does not read, on the line of its problem, a `type: board` note
without exactly one block, a card whose board is no board of the project,
a card whose column the board has not or that is not its state's, and in
a project with cards a `tasks.md` that lost what they need (`backlog`
among the options of `status`, `team` and `assignee` not required, the
card's fields).
