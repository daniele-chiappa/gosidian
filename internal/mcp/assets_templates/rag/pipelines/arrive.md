---
title: Arrive, a document
type: pipeline
description: A file of the IN folder becomes a note of documents/, converted to markdown by docling. The IN folder marks the file done, or error with the run's error.
tags: [{{PROJECT}}]
---

# Arrive, a document

A file of the IN folder in one of the formats below starts a run; its
note is `documents/<its folder in IN>/<its name>`. Converted again (a
new version of the file), the note keeps any field added to its
frontmatter and gets the new text. Its pictures are left out
(`pictures: false`); `ocr: true` reads the text of scanned pages.

```pipeline
trigger: {on: in, kinds: [pdf, docx, doc, pptx, ppt, xlsx, xls, odt, odp, ods, rtf, html, png, jpg, jpeg]}
nodes:
  - id: dest
    use: starlark
    with:
      input: {dir: "{{ trigger.dir }}", stem: "{{ trigger.stem }}"}
      code: |
        d = (input["dir"] or "").strip("/")
        output = "documents/" + (d + "/" if d else "") + input["stem"]
  - id: convert
    use: docling.convert
    timeout: 30m
    with: {file: "{{ trigger.file }}", pictures: false, ocr: true}
  - id: write
    use: note.write
    after: [dest, convert]
    with:
      path: "{{ dest }}"
      title: "{{ trigger.stem }}"
      body: "{{ convert.markdown }}"
      frontmatter: {pages: "{{ convert.pages }}"}
      frontmatter_merge: true
```
