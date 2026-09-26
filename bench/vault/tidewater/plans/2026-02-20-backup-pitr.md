---
title: Backups with point-in-time recovery
description: Set up WAL-G base backups and continuous archiving for pg-main.
tags: [tidewater, type:plan, status:done, topic:database, topic:ops]
type: plan
status: done
author: Luca Ferri
created: 2026-02-20
updated: 2026-03-03
---

# Backups with point-in-time recovery

## Context
Decision in [[tidewater/memory/decisions#ADR-007]].

## Solution
- WAL-G on `kelp-3`, nightly base backup at 02:30 UTC.
- Archive target: the `pg-backups` bucket on Seabed object storage, retention as in the ADR.
- Monthly restore drill on staging.

## Outcome
Live since 2026-02-26. First drill on 2026-03-03 restored to a point 40 minutes earlier in 11 minutes. Procedure: [[tidewater/skills/restore-postgres-backup]].
