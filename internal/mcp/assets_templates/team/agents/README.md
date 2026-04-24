---
title: {{PROJECT}} — agents
description: Specialised agents active on {{PROJECT}}. The team template pre-populates the three canonical roles; duplicate a file when adding a new one.
tags: [{{PROJECT}}, type:index, topic:meta]
type: index
updated: {{TODAY}}
---

# Agents — {{PROJECT}}

An **agent** is a role descriptor: who is responsible for which part
of the work, which files they load first on a new session, which
invariants they must preserve, and what they hand off to others.

## Roles pre-populated by the team template

- [[{{PROJECT}}/agents/backend-engineer]] — server-side code, data
  model, auth, observability
- [[{{PROJECT}}/agents/frontend-engineer]] — HTML/CSS templates,
  accessibility, client-side behaviour
- [[{{PROJECT}}/agents/devops]] — containers, CI/CD, env config,
  backups

Duplicate one of these files when introducing a new specialised
role (`data-engineer`, `security-reviewer`, `product-manager`, …).

## Handoff

Control passes between agents via the `memory_create_handoff` MCP
tool. The receiving agent calls `memory_pending_handoffs(for_agent)`
at session start to collect pending items.

## When to create a new agent

Only when a recurring domain emerges: 2+ consecutive tasks where you
reload the same 3–5 rules / conventions of that domain. Otherwise the
bootstrap context + project README are enough.
