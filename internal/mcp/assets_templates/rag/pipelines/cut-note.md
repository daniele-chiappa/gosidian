---
title: Cut a note
type: pipeline
description: One note of documents/ cut into paragraphs (note.sections) and stored in the collection documents of the database plugin, in place of its paragraphs of before.
tags: [{{PROJECT}}]
---

# Cut a note

Input: `note`, the note's path. Its paragraphs of before go first, so a
paragraph taken out of the note leaves the collection too; the
collection is made with the database's default model at the first
store. The paragraphs go 200 at a time.

```pipeline
trigger: {on: manual}
nodes:
  - id: sections
    use: note.sections
    with: {path: "{{ trigger.note }}", levels: [paragraph]}
  - id: old
    use: database.delete
    after: [sections]
    on_error: continue
    with: {collection: documents, filter: {doc: "{{ sections.doc }}"}}
  - id: batches
    use: starlark
    after: [sections, old]
    with:
      input: "{{ sections.chunks }}"
      code: |
        c = input or []
        output = [c[i:i + 200] for i in range(0, len(c), 200)]
  - id: store
    use: database.upsert
    after: [batches]
    each: "{{ batches }}"
    with: {collection: documents, chunks: "{{ item }}"}
```
