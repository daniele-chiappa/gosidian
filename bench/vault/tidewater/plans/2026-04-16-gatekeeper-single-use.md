---
title: Single-use entry tokens in Gatekeeper
description: Consume each QR token once, atomically, at any gate.
tags: [tidewater, type:plan, status:done, topic:gatekeeper, topic:entry]
type: plan
status: done
author: Ivo Petrov
created: 2026-04-16
updated: 2026-05-07
---

# Single-use entry tokens in Gatekeeper

## Context
BUG-017: the same admission was accepted at two gates within two seconds.

## Solution
- Atomic consume in tide-api ([[tidewater/memory/decisions#ADR-011]]).
- Gatekeeper shows "already used" with the time and gate of the first entry.
- Offline mode keeps a local list and reconciles; conflicts go to the venue report.

## Outcome
v1.9.2 on 2026-05-07. BUG-017 closed.
