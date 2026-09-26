---
title: Idempotent Paylane webhooks
description: Make charge creation and webhook handling idempotent so retries never produce a second charge.
tags: [tidewater, type:plan, status:done, topic:payments]
type: plan
status: done
author: Ivo Petrov
importance: 4
created: 2026-03-02
updated: 2026-03-09
---

# Idempotent Paylane webhooks

## Context
[[tidewater/docs/incidents/2026-02-27-inc-2-duplicate-charges|INC-2]]: retried webhooks created second charges (BUG-011 in [[tidewater/docs/bugs]]).

## Solution
- Idempotency key on every charge request ([[tidewater/memory/decisions#ADR-008]]).
- Table `payment_events` with a unique constraint on the Paylane event id.
- Webhook handler returns 200 for events already processed.

## Outcome
Released in v1.7.0 on 2026-03-09. BUG-011 closed. No duplicate since.
