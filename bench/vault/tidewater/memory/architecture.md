---
title: Tidewater Architecture
description: Components of the tidewater platform, their responsibilities and the external services they depend on.
tags: [tidewater, type:memory, topic:architecture]
type: memory
importance: 4
updated: 2026-06-10
---

# Architecture

## Components

- **tide-api** — Go service, REST + OpenAPI ([[tidewater/memory/decisions#ADR-003]]). Owns orders, events, holds, organizers. Listens on 8080 inside the host network, published through HAProxy.
- **tide-web** — React single-page app for buyers and for the organizer dashboard.
- **tide-worker** — background jobs read from Redis Streams ([[tidewater/memory/decisions#ADR-004]]): ticket PDFs, hold release, payouts, outgoing mail.
- **Gatekeeper** — progressive web app used by venue staff at the gates to validate QR codes against tide-api.

## Data

- **Postgres 16** — primary `pg-main`, one read replica `pg-replica` (hosts in [[tidewater/memory/environments]]). Backups: [[tidewater/memory/decisions#ADR-007]].
- **Redis 7** — streams for jobs, cache for event pages, rate limiting.
- **MinIO** — bucket `tickets-pdf` for generated tickets.

## External services

- **Paylane** — card payments and webhooks. Every charge carries an idempotency key ([[tidewater/memory/decisions#ADR-008]]).
- **MailRiver** — transactional mail. Order confirmations use the template `order-confirm-v2`; account-recovery messages go out through MailRiver with the template `reset-v3`.
- **Nimbus CDN** — static assets of tide-web and event images.

## Flows worth knowing

1. Purchase: tide-web creates a hold, tide-api charges through Paylane, the webhook confirms, tide-worker renders the PDF and mails it.
2. Entry: Gatekeeper sends the QR token, tide-api consumes it once ([[tidewater/memory/decisions#ADR-011]]).
3. Payout: see [[tidewater/plans/2026-04-20-settlement-v1]].
