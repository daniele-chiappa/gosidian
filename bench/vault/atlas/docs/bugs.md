---
title: Atlas Bugs
description: Bug log of atlas.
tags: [atlas, type:doc, topic:bugs]
type: doc
updated: 2026-06-18
---

# Atlas Bugs

## BUG-001 — Hourly ingestion skips orders created exactly on the hour

- **Status**: fixed

## BUG-002 — Sell-through above 100% for events with comps

- **Status**: fixed

## BUG-003 — Organizer report timezone is UTC instead of venue time

- **Status**: fixed

## BUG-004 — Airflow scheduler restarts lose the backfill queue

- **Status**: wontfix

## BUG-005 — Duplicated rows after a backfill without dropping the partition

- **Status**: fixed — see [[atlas/skills/backfill-partition]]
