---
title: Rispondi a una domanda del banco
type: pipeline
description: "Il pilota delle flotte (bench/flotte): un agente fisso (Claude Code, haiku, solo lettura, senza MCP) risponde a una delle 20 domande leggendo solo una copia, in IN, delle note di una cartella del progetto: la conoscenza della squadra o le fonti. La risposta va in una nota con la domanda, la risposta attesa e il passo della fonte, per i giudici."
tags: [fitness]
---

# Rispondi a una domanda del banco

Input: `id`, `domanda`, `attesa`, `evidenza` (il passo della fonte che contiene la risposta), `cartella` (la cartella di IN con la copia delle note, che `pilota.py misura` scrive: `misure/<variante>/conoscenza` o `misure/<variante>/fonti`), `nota` (dove scrivere la risposta), `modello` (default `haiku`). Al più 100 file, il limite dei file di un passo.

```pipeline
trigger: {on: manual}
nodes:
  - id: elenco
    use: in.list
    with: {folder: "{{ trigger.cartella }}", limit: 100}
  - id: file
    use: starlark
    with:
      input: {l: "{{ elenco }}", m: "{{ trigger.modello }}"}
      code: |
        fs = (input["l"] or {}).get("files") or []
        if not fs:
            fail("nessun file nella cartella")
        output = {"count": len(fs), "modello": input["m"] or "haiku"}
  - id: risposta
    use: claude.run
    timeout: 12m
    with:
      model: "{{ file.modello }}"
      access: read
      mcp: none
      files: "{{ elenco.files }}"
      max_turns: 20
      max_cost: 0.5
      system: "Rispondi a domande su attività fisica e allenamento usando solo i file nella cartella in/: sono note scritte da altri. Non aggiungere ciò che sai per conto tuo."
      prompt: |
        Domanda: {{ trigger.domanda }}

        Cerca nei file di in/ (Grep, Glob, Read) e rispondi in italiano, in poche frasi, solo con ciò che i file dicono. Alla fine, su una riga a sé, scrivi «File:» e i nomi dei file che hai usato. Se i file non bastano per rispondere, scrivi NON_TROVATO e perché.
  - id: scrivi
    use: note.write
    with:
      path: "{{ trigger.nota }}"
      title: "{{ trigger.id }}"
      frontmatter:
        domanda: "{{ trigger.id }}"
        cartella: "{{ trigger.cartella }}"
        modello: "{{ file.modello }}"
        note_lette: "{{ file.count }}"
        costo_usd: "{{ risposta.cost }}"
        tags: [fitness, misura]
      body: |
        ## Domanda

        {{ trigger.domanda }}

        ## Attesa

        {{ trigger.attesa }}

        ## Evidenza

        {{ trigger.evidenza }}

        ## Risposta

        {{ risposta.text }}
```
