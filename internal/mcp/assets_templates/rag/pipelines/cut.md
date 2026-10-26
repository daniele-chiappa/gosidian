---
title: Cut, at each commit
type: pipeline
description: At each commit of git sync that touches documents/, each note added or changed is cut again into the collection documents (cut-note), and the paragraphs of a note deleted or moved go away.
tags: [{{PROJECT}}]
---

# Cut, at each commit

A commit too large to list its files (`files_listed: false`) cuts every
note of `documents/` again. Each note is a run of
[[{{PROJECT}}/pipelines/cut-note]].

```pipeline
trigger: {on: commit, folder: documents, match: "*.md"}
nodes:
  - id: files
    use: starlark
    with:
      input: {files: "{{ trigger.files }}", listed: "{{ trigger.files_listed }}"}
      code: |
        keep, gone = [], []
        for f in (input["files"] or []):
            if f["change"] == "deleted":
                gone.append(f["path"])
            else:
                if f["change"] == "renamed" and f.get("from"):
                    gone.append(f["from"])
                keep.append(f["path"])
        output = {"keep": keep, "gone": gone, "all": input["listed"] == False}
  - id: everything
    use: note.list
    when: "{{ files.all }}"
    with: {folder: documents, limit: 5000}
  - id: notes
    use: starlark
    after: [files, everything]
    with:
      input: {files: "{{ files }}", everything: "{{ everything.notes }}"}
      code: |
        if input["files"]["all"]:
            output = [n["path"] for n in (input["everything"] or [])]
        else:
            output = input["files"]["keep"]
  - id: drop
    use: database.delete
    after: [files]
    each: "{{ files.gone }}"
    on_error: continue
    with: {collection: documents, filter: {doc: "{{ item }}"}}
  - id: cut
    use: pipeline.run
    after: [notes]
    each: "{{ notes }}"
    with: {pipeline: pipelines/cut-note, input: {note: "{{ item }}"}, wait: false}
```
