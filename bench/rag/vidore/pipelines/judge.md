---
title: Judge a ViDoRe answer
type: pipeline
description: "The ViDoRe V3 benchmark (bench/vidore): a local model scores an answer note from -10 to +10 against the reference answer, with the relevant pages and the sources the bot had, under the rules of gosidian's RAG benchmarks; the score goes into the note's frontmatter."
tags: [vidore]
---

# Judge a ViDoRe answer

Input: `note` (an answer note of the answer pipeline), `judge` (a model, for example `gemma4:12b-12k`), `reference` (the text of the relevant pages, up to 8000 characters). The note gets `score_<judge>`, `invented_<judge>` and `reason_<judge>`, where `<judge>` is the model's name before the colon.

The rules are those of docs/rag-benchmarks.md: "I don't know" scores 0; what the bot takes from its sources is not invented; leaving something out is not an error.

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
        ctx = part("Context", "")
        output = {"question": part("Question", "Expected"), "expected": part("Expected", "Answer"), "answer": part("Answer", "Sources"),
                  "sources": (ctx[:18000] + ("\n[…]" if len(ctx) > 18000 else "")) or "(none)", "key": input["j"].split(":")[0]}
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
      system: "You judge with rigour the answers of an assistant that answers on a collection of professional documents."
      prompt: |
        The pages of the documents that hold the answer:
        <pages>
        {{ trigger.reference }}
        </pages>

        Question: {{ parts.question }}
        Expected answer: {{ parts.expected }}

        The sources the assistant had to answer:
        <sources>
        {{ parts.sources }}
        </sources>

        The assistant's answer:
        <answer>
        {{ parts.answer }}
        </answer>

        Score the assistant's answer from -10 to 10, comparing it with the expected answer; the pages and the sources serve to tell whether what it says is true. Citations like [1] do not count. The answer may be in another language than the pages: that is not an error.
        Rules, in this order:
        1. If the assistant says it does not know, or that the sources are not enough (NOT_FOUND), the score is 0, even if the pages hold the answer: it is not an error.
        2. Information that is not in the pages but is in the assistant's sources is not invented: it is an error only if it contradicts the pages or the expected answer.
        3. Information that is neither in the pages nor in the assistant's sources is invented.
        4. Leaving out parts of the expected answer makes it incomplete, not wrong: the score goes down but stays positive if what it says is right.
        Score:
        - 10: right and complete, it says what the expected answer says;
        - 1 to 9: right but incomplete, or with parts that are not needed; the more is missing, the lower;
        - 0: it says it does not know, or does not answer the question;
        - -1 to -9: it holds errors or invented information; the more serious, the lower;
        - -10: wrong or invented, and said with confidence.
        Answer only in JSON with three fields: "score" (an integer from -10 to 10), "invented" (true if the answer says things that are neither in the pages nor in the assistant's sources) and "reason" (one sentence).
  - id: verdict
    use: starlark
    after: [judge, parts]
    with:
      input: {j: "{{ judge }}", k: "{{ parts.key }}"}
      code: |
        j = input["j"] or {}
        d = j.get("json") or {}
        k = input["k"]
        if type(d) != "dict" or d.get("score") == None:
            # A reply cut by the context (finish_reason length) may hold the score before the reason: take it from the text.
            t = j.get("text") or ""
            i = t.find('"score"')
            if i >= 0:
                rest = t[i + 7:].lstrip(" :\n\t")
                n = 0
                for ch in rest.elems():
                    if ch.isdigit() or (n == 0 and ch == "-"):
                        n += 1
                    else:
                        break
                if n > 0 and rest[:n] != "-":
                    d = {"score": int(rest[:n]), "invented": '"invented": true' in t, "reason": "(the judge's reply was cut: score read from its text)"}
        ok = type(d) == "dict" and d.get("score") != None
        sc = d.get("score") if ok else None
        if ok and type(sc) == "string":
            sc = int(sc.strip()) if sc.strip().lstrip("-").isdigit() else None
        ok = ok and sc != None
        fm = {}
        if ok:
            fm = {"score_" + k: max(-10, min(10, int(sc))), "invented_" + k: d.get("invented") == True, "reason_" + k: str(d.get("reason") or "")[:500]}
        output = {"ok": ok, "fm": fm}
  - id: write
    use: note.write
    when: "{{ verdict.ok }}"
    with: {path: "{{ trigger.note }}", frontmatter: "{{ verdict.fm }}", frontmatter_merge: true}
```
