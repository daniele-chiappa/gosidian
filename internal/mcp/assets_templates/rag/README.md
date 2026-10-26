---
title: {{PROJECT}}
description: Questions answered from the documents of {{PROJECT}}, with their sources. Scaffolded on {{TODAY}} from the rag template.
tags: [{{PROJECT}}, type:index, topic:meta]
type: index
updated: {{TODAY}}
---

# {{PROJECT}}

Documents dropped in the IN folder become notes in `documents/`, are cut
into paragraphs and stored with their embeddings, and the bot answers
questions from them, citing its sources.

## How it flows

1. **A file arrives** in the IN folder (the web UI's IN window, a bound
   source of the storage plugin, or a copy into `IN/` on the server).
   [[{{PROJECT}}/pipelines/arrive]] converts it with docling into a note
   of `documents/`; a Markdown file with its images goes through
   [[{{PROJECT}}/pipelines/arrive-markdown]], which converts the images
   with the images plugin.
2. **git sync commits** the new or changed notes, and
   [[{{PROJECT}}/pipelines/cut]] cuts each one again into paragraphs
   ([[{{PROJECT}}/pipelines/cut-note]]) and stores them in the
   collection `documents` of the database plugin. A deleted note's
   paragraphs go away.
3. **A question** goes to [[{{PROJECT}}/pipelines/answer]]: the
   paragraphs most like it, then the model of the server, which answers
   only from them and cites them as [n]. The bot
   [[{{PROJECT}}/bots/assistant]] asks it for every message, through
   [[{{PROJECT}}/pipelines/bot]], and keeps the conversations in `chat/`.

## To start

- Turn the project's **pipelines on** in its settings: that also gives
  it its IN folder. The server needs git sync on (the rag preset turns
  it on, without a remote).
- Drop a few files in the IN folder, then ask: start
  [[{{PROJECT}}/pipelines/answer]] with `{"question": "…"}` from the Runs
  window, or open the bot's note and chat.
- Every pipeline is a note: change a prompt, the number of sources (`k`)
  or the model in its block, and the next run uses it.

## Files

- [hot.md](hot.md) — current focus
- [log.md](log.md) — append-only activity log
- `pipelines/` — the pipelines above
- `bots/` — the bot
- `documents/` — the notes made from the files (written by the pipelines)
- `chat/` — the conversations (written by the bot)
