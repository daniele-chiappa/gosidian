---
title: Virtual waiting room
description: Queue page that admits buyers in batches during high-demand drops.
tags: [tidewater, type:plan, status:done, topic:scalability, topic:checkout]
type: plan
status: done
author: Marta Ruiz
importance: 4
created: 2026-05-04
updated: 2026-06-01
---

# Virtual waiting room

## Context
The drop of 2026-04-25 (3,000 buyers in the first minute) produced timeouts on tide-api and Paylane rate-limit errors. Decision: [[tidewater/memory/decisions#ADR-012]].

## Solution
- High-demand drops route buyers to a queue page served from Nimbus CDN.
- A token bucket admits 500 buyers per minute; admitted buyers get a signed pass valid 15 minutes.
- Workers scaled ahead of time with [[tidewater/skills/scale-worker-pool]].

## Outcome
First use on 2026-05-30: 12,000 buyers queued, zero timeouts.
