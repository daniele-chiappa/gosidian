---
title: Cut a ViDoRe document
type: pipeline
description: "The ViDoRe V3 benchmark (bench/vidore): cuts one document note with note.sections and loads its chunks into a collection of the vector archive. The pages index cuts sections (a section is a page), the paragraphs index paragraphs."
tags: [vidore]
---

# Cut a ViDoRe document

Input: `note` (a document note written by `convert.py`), `collection`, `levels` (`[paragraph]` or `[section]`), `max_chars` (a longer section is cut in parts), `model` (the embedding model, used when the collection is created).

Each chunk carries the document's `corpus`, `doc_id` and `language`; its `section` is the page (`Page 12`), which is how the metrics find the page again.

```pipeline
trigger: {on: manual}
nodes:
  - id: doc
    use: note.read
    with: {path: "{{ trigger.note }}"}
  - id: meta
    use: starlark
    with:
      input: {fm: "{{ doc.frontmatter }}"}
      code: |
        fm = input["fm"] or {}
        output = {"corpus": str(fm.get("corpus") or ""), "doc_id": str(fm.get("doc_id") or ""), "language": str(fm.get("language") or "")}
  - id: cut
    use: note.sections
    with:
      path: "{{ trigger.note }}"
      levels: "{{ trigger.levels }}"
      max_chars: "{{ trigger.max_chars }}"
      media: keep
      meta: "{{ meta }}"
  - id: load
    use: vec-qdrant.upsert
    retry: {attempts: 3, backoff: 30s}
    timeout: 60m
    with: {collection: "{{ trigger.collection }}", model: "{{ trigger.model }}", chunks: "{{ cut.chunks }}"}
```
