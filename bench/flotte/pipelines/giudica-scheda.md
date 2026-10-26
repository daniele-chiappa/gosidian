---
title: Giudica una scheda del banco
type: pipeline
description: "Il pilota delle flotte (bench/flotte): un modello locale controlla la scheda di una persona contro le sue verifiche (sicurezza, progressione, linee guida, adattamento, fonti), una per una, con sì, in parte o no; gli esiti vanno nel frontmatter di una nota di misura."
tags: [fitness]
---

# Giudica una scheda del banco

Input: `scheda` (la nota della scheda), `persona` (il testo della persona), `verifiche` (una lista di `{id, testo}`), `judge` (un modello), `nota` (la nota di misura). La nota riceve `esiti_<giudice>` (`id` → `si`, `parziale`, `no`), `motivi_<giudice>` e `punti_<giudice>` (sì 1, in parte 0,5, no 0, sul totale delle verifiche).

```pipeline
trigger: {on: manual}
nodes:
  - id: scheda
    use: note.read
    with: {path: "{{ trigger.scheda }}"}
  - id: prep
    use: starlark
    with:
      input: {body: "{{ scheda.body }}", v: "{{ trigger.verifiche }}", j: "{{ trigger.judge }}"}
      code: |
        b = input["body"]
        lines = ["- %s: %s" % (x["id"], x["testo"]) for x in input["v"]]
        output = {"text": b[:20000] + ("\n[…]" if len(b) > 20000 else ""), "checks": "\n".join(lines),
                  "ids": [x["id"] for x in input["v"]], "key": input["j"].split(":")[0]}
  - id: judge
    use: llm.chat
    after: [prep]
    timeout: 20m
    with:
      model: "{{ trigger.judge }}"
      json: true
      max_tokens: 1500
      temperature: 0
      reasoning_effort: none
      system: "You check, with rigour, a beginner's workout plan written for one person against a list of checks drawn from physical activity guidelines and reviews."
      prompt: |
        The person:
        <person>
        {{ trigger.persona }}
        </person>

        The plan written for this person:
        <plan>
        {{ prep.text }}
        </plan>

        The checks, each with its id:
        {{ prep.checks }}

        For each check, say whether the plan meets it: "si" (it does, clearly), "parziale" (in part, or only implied), "no" (it does not, or the plan says nothing about it). Judge only what the plan says; the plan may be in Italian.
        Answer only in JSON: {"esiti": [{"id": "<the check's id>", "esito": "si" | "parziale" | "no", "motivo": "<one short sentence>"}]}, one entry per check, in the order of the checks.
  - id: verdict
    use: starlark
    after: [judge, prep]
    with:
      input: {j: "{{ judge }}", ids: "{{ prep.ids }}", k: "{{ prep.key }}"}
      code: |
        d = (input["j"] or {}).get("json") or {}
        rows = d.get("esiti") if type(d) == "dict" else None
        es, mo = {}, {}
        for r in rows or []:
            if type(r) == "dict" and r.get("id") in input["ids"] and r.get("esito") in ("si", "parziale", "no"):
                es[r["id"]] = r["esito"]
                mo[r["id"]] = str(r.get("motivo") or "")[:300]
        ok = len(es) == len(input["ids"])
        pts = 0.0
        for v in es.values():
            pts += 1.0 if v == "si" else (0.5 if v == "parziale" else 0.0)
        k = input["k"]
        output = {"ok": ok, "fm": {"esiti_" + k: es, "motivi_" + k: mo, "punti_" + k: pts, "verifiche": len(input["ids"])}}
  - id: write
    use: note.write
    when: "{{ verdict.ok }}"
    with: {path: "{{ trigger.nota }}", title: "Scheda: verifiche", frontmatter: "{{ verdict.fm }}", frontmatter_merge: true}
```
