---
title: Ticket PDF generation
description: Render one PDF per order with a QR code per admission and store it in MinIO.
tags: [tidewater, type:plan, status:done, topic:worker, topic:tickets]
type: plan
status: done
author: Ivo Petrov
created: 2026-02-05
updated: 2026-02-14
---

# Ticket PDF generation

## Solution
- tide-worker consumes `orders.paid` from Redis Streams.
- One page per admission, QR token of 22 characters, event artwork from Nimbus CDN.
- Stored in the MinIO bucket `tickets-pdf`, linked from the confirmation mail.

## Outcome
Done in v1.3.0. Re-rendering a lost PDF: [[tidewater/skills/reissue-ticket-pdf]].
