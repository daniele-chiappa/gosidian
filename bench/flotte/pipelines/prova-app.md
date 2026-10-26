---
title: Prova l'app del banco
type: pipeline
description: "Il pilota delle flotte (bench/flotte): costruisce l'app di agents-fitness da main (test e build), la pubblica in anteprima e la prova come una persona, sul computer e sul telefono, con i passi che dà lo script (le cinque persone, il catalogo); gli esiti vanno in una nota di misura."
tags: [fitness]
---

# Prova l'app del banco

Input: `ref` (default `main`), `passi` (i passi di `preview.check`), `nota` (la nota di misura).

```pipeline
trigger: {on: manual}
nodes:
  - id: build
    use: exec.run
    timeout: 16m
    with:
      repo: {name: agents-fitness, ref: "{{ trigger.ref }}"}
      script: |
        cd repo
        npm test
        npm run build
        python3 -m zipfile -c ../out/site.zip dist/.
  - id: pronta
    use: starlark
    with:
      input: {b: "{{ build }}"}
      code: |
        b = input["b"] or {}
        output = {"ok": b.get("exit_code") == 0 and len(b.get("files") or []) > 0}
  - id: publish
    use: preview.publish
    when: "{{ pronta.ok }}"
    with: {site: "{{ build.files.0.path }}", ttl: 3d, title: "agents-fitness {{ trigger.ref }}"}
  - id: computer
    use: preview.check
    when: "{{ pronta.ok }}"
    with: {id: "{{ publish.id }}", steps: "{{ trigger.passi }}", timeout: 300}
  - id: telefono
    use: preview.check
    when: "{{ pronta.ok }}"
    with: {id: "{{ publish.id }}", steps: "{{ trigger.passi }}", viewport: {width: 390, height: 844}, timeout: 300}
  - id: esito
    use: starlark
    after: [build, pronta, computer, telefono]
    with:
      input: {b: "{{ build }}", c: "{{ computer }}", t: "{{ telefono }}"}
      code: |
        def one(x):
            x = x or {}
            st = x.get("steps") or []
            return {"ok": x.get("ok") == True, "passi_ok": len([s for s in st if s.get("ok")]), "passi": len(st),
                    "console": len(x.get("console") or []), "richieste_fallite": len(x.get("failed_requests") or [])}
        c, t = one(input["c"]), one(input["t"])
        out = (input["b"] or {}).get("stdout") or ""
        output = {"fm": {"build_exit": (input["b"] or {}).get("exit_code"), "computer": c, "telefono": t},
                  "body": "## Build\n\n```text\n" + out[-3000:] + "\n```\n"}
  - id: scrivi
    use: note.write
    with: {path: "{{ trigger.nota }}", title: "Prova dell'app", body: "{{ esito.body }}", frontmatter: "{{ esito.fm }}"}
```
