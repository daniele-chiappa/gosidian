---
title: INC-2 — Duplicate Paylane charges
description: Postmortem of INC-2: retried webhooks produced second charges for 41 orders.
tags: [tidewater, type:doc, topic:incidents]
type: doc
created: 2026-02-27
---

# INC-2 — Duplicate Paylane charges

- **When**: 2026-02-27, 09:10–13:40 UTC
- **Impact**: 41 orders charged twice, all refunded the same day.
- **Root cause**: BUG-011 in [[tidewater/docs/bugs]] — when tide-api answered a Paylane webhook slower than 10 seconds, Paylane retried and our handler created a second charge.
- **Timeline**: 09:10 slow database during a migration; 09:40 first support ticket; 11:05 cause found; 13:40 handler patched.
- **Follow-ups**: idempotency keys ([[tidewater/memory/decisions#ADR-008]]); runbook [[tidewater/skills/investigate-double-charge]].
