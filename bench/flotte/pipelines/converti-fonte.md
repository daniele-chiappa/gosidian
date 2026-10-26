---
title: Converti una fonte
type: pipeline
description: "Il pilota delle flotte (bench/flotte): un PDF del corpus, caricato in IN, diventa una nota di fitness/fonti/ con docling; il frontmatter (autori, anno, DOI, licenza, SHA-256) viene da corpus.json."
tags: [fitness]
---

# Converti una fonte

Input: `file` (un file di IN), `dest` (la nota, senza `.md`), `title`, `frontmatter` (i campi della nota). Una fonte convertita di nuovo prende il testo e il frontmatter nuovi.

```pipeline
trigger: {on: manual}
nodes:
  - id: docling
    use: docling.convert
    timeout: 30m
    with: {file: "{{ trigger.file }}", pictures: false, ocr: false}
  - id: testo
    use: starlark
    with:
      input: {d: "{{ docling }}", fm: "{{ trigger.frontmatter }}", file: "{{ trigger.file }}"}
      code: |
        d = input["d"] or {}
        if not d.get("markdown"):
            fail("nessun testo da docling")
        fm = dict(input["fm"]) if type(input["fm"]) == "dict" else {}
        fm["pagine"] = d.get("pages") or 0
        fm["fonte_in"] = input["file"]
        output = {"body": d["markdown"], "fm": fm}
  - id: scrivi
    use: note.write
    with: {path: "{{ trigger.dest }}", title: "{{ trigger.title }}", body: "{{ testo.body }}", frontmatter: "{{ testo.fm }}"}
```
