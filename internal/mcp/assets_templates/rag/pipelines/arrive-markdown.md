---
title: Arrive, Markdown with its images
type: pipeline
description: A Markdown file of the IN folder, with the images it links beside it, becomes a note of documents/, its images stored as attachments after the images plugin converted them without loss.
tags: [{{PROJECT}}]
---

# Arrive, Markdown with its images

For documentation exported as Markdown with its pictures: the tree of
the IN folder is kept under `documents/`, a link to another `.md`
becomes a wikilink, and each image gets an image note beside the
document. A vision model can then describe the images, so that their
text is searched too: add a step
`{use: llm.describe_media, with: {note: "{{ import.path }}"}}`.

```pipeline
trigger: {on: in, kinds: [md]}
nodes:
  - id: import
    use: in.import
    with: {file: "{{ trigger.file }}", dest: documents, convert: images.convert, convert_with: {format: webp}}
```
