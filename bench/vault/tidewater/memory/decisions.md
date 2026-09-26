---
title: Tidewater Decisions
description: Architecture decision records of tidewater, newest last.
tags: [tidewater, type:memory, topic:decisions]
type: memory
importance: 5
updated: 2026-05-02
---

# Decisions

## ADR-001 — Go for the API

- **Date**: 2026-01-08 · **Status**: accepted
- Static binaries, simple deployment on VMs, team experience.

## ADR-002 — Server-side sessions in Redis

- **Date**: 2026-01-12 · **Status**: superseded by ADR-009
- Session ids in a cookie, session data in Redis with a 24 h TTL.

## ADR-003 — REST with OpenAPI, no GraphQL

- **Date**: 2026-01-15 · **Status**: accepted
- The SPA and Gatekeeper need a handful of stable resources; GraphQL adds a gateway and caching problems for no gain. A later spike confirmed it: [[tidewater/plans/2026-02-12-graphql-gateway-spike]].

## ADR-004 — Redis Streams for background jobs

- **Date**: 2026-01-20 · **Status**: accepted
- Redis Streams with consumer groups instead of RabbitMQ: one less service to run, Redis is already there. Revisited once, see [[tidewater/plans/2026-03-05-rabbitmq-migration]].

## ADR-005 — Compose on four VMs, no Kubernetes

- **Date**: 2026-01-22 · **Status**: accepted
- Four hosts, one operator. Kubernetes was rejected: the control plane would cost more attention than the whole platform. Services run with Compose, driven by `tidectl`.

## ADR-006 — At most 8 tickets per order

- **Date**: 2026-02-10 · **Status**: accepted
- Anti-scalping measure agreed in the [[tidewater/docs/meetings/2026-02-10-weekly|weekly of 2026-02-10]]. Organizers can lower the limit per event, never raise it.

## ADR-007 — Postgres backups with WAL-G and point-in-time recovery

- **Date**: 2026-02-18 · **Status**: accepted
- Nightly base backup to object storage, continuous WAL archiving, WAL archives retained for 14 days. Procedure: [[tidewater/skills/restore-postgres-backup]].

## ADR-008 — Idempotency keys on every Paylane charge

- **Date**: 2026-03-01 · **Status**: accepted
- Each charge request carries a key derived from the order id and attempt; the webhook handler ignores keys already settled. Born from [[tidewater/docs/incidents/2026-02-27-inc-2-duplicate-charges|INC-2]].

## ADR-009 — Stateless access tokens

- **Date**: 2026-03-20 · **Status**: accepted, supersedes ADR-002
- JWT access tokens valid 15 minutes plus rotating refresh tokens. Removes the session lookup from every request.

## ADR-010 — Processing costs go to the buyer

- **Date**: 2026-04-06 · **Status**: accepted
- Paylane processing costs are passed on to the buyer as a service charge, displayed before payment and listed on the receipt. Venues receive the full face value.

## ADR-011 — Single-use QR tokens

- **Date**: 2026-04-15 · **Status**: accepted
- Entry validation consumes the token atomically (`UPDATE entries SET consumed_at = now() WHERE token = $1 AND consumed_at IS NULL`). A second attempt at any gate gets "already used" with the time and gate of the first. Fixes the race of BUG-017.

## ADR-012 — Virtual waiting room for high-demand drops

- **Date**: 2026-05-02 · **Status**: accepted
- When a drop is flagged high-demand, buyers wait in a queue page and are admitted in batches of 500 per minute, so tide-api and Paylane see a bounded rate. Plan: [[tidewater/plans/2026-05-04-waiting-room]].
