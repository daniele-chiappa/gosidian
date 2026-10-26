---
title: Giudica una risposta del banco
type: pipeline
description: "Il pilota delle flotte (bench/flotte): un modello locale dà a una risposta della pipeline rispondi un voto da -10 a +10 contro la risposta attesa e il passo della fonte, con le regole dei banchi RAG di gosidian; il voto va nel frontmatter della nota."
tags: [fitness]
---

# Giudica una risposta del banco

Input: `note` (una nota di risposta della pipeline rispondi), `judge` (un modello, per esempio `gemma4:12b-12k`). La nota riceve `score_<giudice>`, `invented_<giudice>` e `reason_<giudice>`, dove `<giudice>` è il nome del modello prima dei due punti.

Le regole sono quelle di docs/rag-benchmarks.md: «non lo so» vale 0; lasciare fuori qualcosa non è un errore; un'aggiunta è un errore solo se contraddice l'attesa o la fonte.

```pipeline
trigger: {on: manual}
nodes:
  - id: answer
    use: note.read
    with: {path: "{{ trigger.note }}"}
  - id: parts
    use: starlark
    with:
      input: {body: "{{ answer.body }}", j: "{{ trigger.judge }}"}
      code: |
        b = input["body"]
        def part(name, nxt):
            i = b.find("## " + name + "\n")
            if i < 0:
                return ""
            rest = b[i + len(name) + 4:]
            j = rest.find("\n## " + nxt + "\n") if nxt else -1
            return (rest if j < 0 else rest[:j]).strip()
        output = {"question": part("Domanda", "Attesa"), "expected": part("Attesa", "Evidenza"), "evidence": part("Evidenza", "Risposta"),
                  "answer": part("Risposta", "")[:6000], "key": input["j"].split(":")[0]}
  - id: judge
    use: llm.chat
    after: [parts]
    timeout: 15m
    with:
      model: "{{ trigger.judge }}"
      json: true
      max_tokens: 500
      temperature: 0
      reasoning_effort: none
      system: "You judge with rigour the answers of an assistant that answers questions on physical activity and training from a set of notes."
      prompt: |
        The passage of the source document that holds the answer:
        <passage>
        {{ parts.evidence }}
        </passage>

        Question: {{ parts.question }}
        Expected answer: {{ parts.expected }}

        The assistant's answer (it may end with the names of the files it used):
        <answer>
        {{ parts.answer }}
        </answer>

        Score the assistant's answer from -10 to 10, comparing it with the expected answer; the passage serves to tell whether what it says is true. The answer, the question and the passage may be in different languages: that is not an error.
        Rules, in this order:
        1. If the assistant says it does not know, or that its notes are not enough (NON_TROVATO), the score is 0: it is not an error.
        2. Information that is neither in the expected answer nor in the passage is not an error by itself: it is an error only if it contradicts them, or is plainly false.
        3. Leaving out parts of the expected answer makes it incomplete, not wrong: the score goes down but stays positive if what it says is right.
        4. A wrong number (minutes, days, sets, ages) where the expected answer has one is an error.
        Score:
        - 10: right and complete, it says what the expected answer says;
        - 1 to 9: right but incomplete, or with parts that are not needed; the more is missing, the lower;
        - 0: it says it does not know, or does not answer the question;
        - -1 to -9: it holds errors; the more serious, the lower;
        - -10: wrong, and said with confidence.
        Answer only in JSON with three fields: "score" (an integer from -10 to 10), "invented" (true if the answer states something that contradicts the expected answer or the passage) and "reason" (one sentence).
  - id: verdict
    use: starlark
    after: [judge, parts]
    with:
      input: {j: "{{ judge }}", k: "{{ parts.key }}"}
      code: |
        j = input["j"] or {}
        d = j.get("json") or {}
        k = input["k"]
        sc = d.get("score") if type(d) == "dict" else None
        if type(sc) == "string":
            sc = int(sc.strip()) if sc.strip().lstrip("-").isdigit() else None
        ok = sc != None
        fm = {}
        if ok:
            fm = {"score_" + k: max(-10, min(10, int(sc))), "invented_" + k: d.get("invented") == True, "reason_" + k: str(d.get("reason") or "")[:500]}
        output = {"ok": ok, "fm": fm}
  - id: write
    use: note.write
    when: "{{ verdict.ok }}"
    with: {path: "{{ trigger.note }}", frontmatter: "{{ verdict.fm }}", frontmatter_merge: true}
```
