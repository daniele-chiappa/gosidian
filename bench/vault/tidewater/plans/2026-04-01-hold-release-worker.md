---
title: Hold release worker
description: Give back the places of unpaid holds automatically.
tags: [tidewater, type:plan, status:done, topic:worker, topic:inventory]
type: plan
status: done
author: Ivo Petrov
created: 2026-04-01
updated: 2026-04-21
---

# Hold release worker

## Context
Places stayed blocked when a purchase stopped before payment: the inventory only recovered at the nightly cleanup.

## Solution
- tide-worker scans holds every 30 seconds.
- Unpaid holds lapse after ten minutes; their places return to the inventory and the event page cache is invalidated.
- A late Paylane confirmation on a lapsed hold triggers an automatic refund.

## Outcome
v1.9.0 on 2026-04-21. Inventory is accurate within a minute.
