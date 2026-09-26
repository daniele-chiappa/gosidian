---
title: Service charge rollout
description: Show the processing charge before payment and on receipts.
tags: [tidewater, type:plan, status:done, topic:checkout, topic:payments]
type: plan
status: done
author: Sara Lind
created: 2026-04-08
updated: 2026-04-22
---

# Service charge rollout

## Context
[[tidewater/memory/decisions#ADR-010]]: Paylane processing costs are passed on to the buyer.

## Solution
- The purchase summary lists face value and service charge separately.
- Receipts and PDFs show both amounts.
- Payout reports keep the face value only.

## Outcome
v1.9.0. Support tickets about the extra line: 3 in the first week.
