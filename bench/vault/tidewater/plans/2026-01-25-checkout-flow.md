---
title: Purchase path v1
description: Hold, charge, confirm: the first end-to-end purchase path.
tags: [tidewater, type:plan, status:done, topic:checkout, topic:payments]
type: plan
status: done
author: Sara Lind
created: 2026-01-25
updated: 2026-02-03
---

# Purchase path v1

## Context
Buyers pick an event, choose places, pay. Paylane handles the charge.

## Solution
1. tide-web creates a hold through `POST /holds`.
2. tide-api creates the Paylane charge and returns the 3-D Secure redirect.
3. The Paylane webhook marks the order paid; tide-worker renders the PDF.

## Outcome
Live in v1.2.0 (2026-02-03). The webhook path later needed idempotency, see [[tidewater/plans/2026-03-02-webhook-idempotency]].
