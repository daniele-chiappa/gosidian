---
title: Tidewater Log
description: Chronological log of releases, decisions, incidents and operations.
tags: [tidewater, type:log]
type: log
updated: 2026-06-24
---

# Tidewater Log

Append-only, newest last. One line per event: date — kind — what.

- 2026-01-08 — decision — Go for the API (ADR-001).
- 2026-01-12 — decision — sessions in Redis (ADR-002).
- 2026-01-15 — decision — REST with OpenAPI (ADR-003).
- 2026-01-19 — release — v1.0.0: events, orders, health checks.
- 2026-01-20 — decision — Redis Streams for jobs (ADR-004).
- 2026-01-22 — decision — Compose on four VMs (ADR-005).
- 2026-01-26 — release — v1.1.0: organizer accounts.
- 2026-01-30 — incident — INC-1, event pages failing for 25 minutes.
- 2026-02-03 — release — v1.2.0: purchase path v1.
- 2026-02-10 — decision — at most 8 admissions per order (ADR-006).
- 2026-02-14 — release — v1.3.0: ticket PDFs.
- 2026-02-18 — decision — WAL-G backups with point-in-time recovery (ADR-007).
- 2026-02-19 — ops — rotated the Paylane sandbox key.
- 2026-02-20 — release — v1.4.0: dashboard totals fix.
- 2026-02-26 — ops — backups live on `kelp-3`.
- 2026-02-27 — incident — INC-2, duplicate Paylane charges on 41 orders.
- 2026-03-01 — decision — idempotency keys on every charge (ADR-008).
- 2026-03-02 — release — v1.6.0: per-event order limit.
- 2026-03-04 — release — v1.6.1: time-zone fix for start times (BUG-008).
- 2026-03-09 — release — v1.7.0: idempotent webhooks, BUG-011 closed.
- 2026-03-17 — meeting — seat maps launch moved to April.
- 2026-03-20 — decision — stateless access tokens (ADR-009).
- 2026-04-02 — release — v1.8.0: seat maps.
- 2026-04-06 — decision — processing costs to the buyer (ADR-010).
- 2026-04-09 — release — v1.8.1: stateless access tokens.
- 2026-04-15 — ops — rotated the Paylane prod secret key.
- 2026-04-15 — decision — single-use QR tokens (ADR-011).
- 2026-04-21 — release — v1.9.0: hold release worker and service charge.
- 2026-04-25 — ops — drop overload: 3,000 buyers in the first minute, timeouts.
- 2026-05-02 — decision — virtual waiting room (ADR-012).
- 2026-05-04 — ops — first automatic payout run.
- 2026-05-07 — release — v1.9.2: single-use entry tokens, BUG-017 closed.
- 2026-05-16 — ops — TLS certificate expired for 20 minutes, renewed by hand.
- 2026-05-20 — ops — rotated the Paylane sandbox key ([[tidewater/skills/rotate-paylane-keys]]).
- 2026-05-26 — release — v1.9.4: virtual waiting room.
- 2026-05-27 — ops — automatic TLS renewal live.
- 2026-05-29 — incident — INC-3, Redis out of memory, 50 minutes without PDFs.
- 2026-05-30 — ops — first high-demand drop behind the waiting room, zero timeouts.
- 2026-06-04 — release — v1.10.0: waiting room pass expiry, rate limit on holds.
- 2026-06-09 — incident — INC-4, Nimbus CDN outage in Europe.
- 2026-06-12 — ops — payments v2 at 1% of prod traffic.
- 2026-06-24 — ops — payments v2 at 10% of prod traffic.
