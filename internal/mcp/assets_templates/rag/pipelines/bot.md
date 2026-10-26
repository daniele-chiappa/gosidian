---
title: The bot's pipeline
type: pipeline
description: A message of the bot asks answer and appends the turn to its conversation in chat/; a rating goes into the conversation's frontmatter.
tags: [{{PROJECT}}]
---

# The bot's pipeline

The bot is [[{{PROJECT}}/bots/assistant]]. A message (`kind: message`)
starts [[{{PROJECT}}/pipelines/answer]] with its text; the answer the chat
shows is the output of `reply`, with `turn`, the id of this run, which a
rating names.

```pipeline
trigger: {on: chat}
answer: reply
nodes:
  - id: kind
    use: starlark
    with:
      input: {kind: "{{ trigger.kind }}", session: "{{ trigger.session }}"}
      code: |
        k = input["kind"] or "message"
        output = {"message": k == "message", "rating": k == "rating", "note": "chat/" + (input["session"] or "none")}
  - id: ask
    use: pipeline.run
    after: [kind]
    when: "{{ kind.message }}"
    timeout: 5m
    with: {pipeline: pipelines/answer, input: {question: "{{ trigger.message }}"}}
  - id: reply
    use: starlark
    after: [ask]
    with:
      input: {r: "{{ ask.outputs.reply }}", turn: "{{ run.id }}"}
      code: |
        r = input["r"] or {}
        output = {"text": r.get("text") or "", "sources": r.get("sources") or [], "turn": input["turn"]}
  - id: turn
    use: note.write
    after: [reply]
    with:
      path: "{{ kind.note }}"
      mode: append
      title: "Conversation {{ trigger.session }}"
      frontmatter: {type: chat-conversation, session: "{{ trigger.session }}"}
      body: "## {{ run.id }}\n\n**Question**: {{ trigger.message }}\n\n**Answer**: {{ reply.text }}"
  - id: rate
    use: note.write
    after: [kind]
    when: "{{ kind.rating }}"
    with:
      path: "{{ kind.note }}"
      frontmatter_merge: true
      frontmatter: {ratings: {"{{ trigger.turn }}": "{{ trigger.rating }}"}}
```
