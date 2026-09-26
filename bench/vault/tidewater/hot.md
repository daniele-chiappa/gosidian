---
title: Tidewater Hot State
description: What the team is working on right now. Short, rewritten often.
tags: [tidewater, type:hot, pinned]
type: hot
updated: 2026-06-28
---

# Hot state — 2026-06-28

## Current focus

- **Payments v2** — migration to the Paylane v2 API with 3-D Secure 2, behind the flag `ff_payments_v2`. Plan: [[tidewater/plans/2026-05-12-payments-v2]]. Staging done, 10% of prod traffic since 2026-06-24.
- **INC-3 follow-ups** — Redis memory alerting and eviction policy, see [[tidewater/plans/2026-06-02-inc-3-followups]].
- **Organizer dashboard** — new sales charts, [[tidewater/plans/2026-05-26-organizer-dashboard]].

## Next

- Refund self-service (draft plan) after payments v2 reaches 100%.
- Email deliverability review with MailRiver.

## Watch out

- Deploy freeze rule in [[tidewater/memory/conventions]] applies to the payments rollout too.
- On-call rotation: [[tidewater/agents/ops-oncall]].
