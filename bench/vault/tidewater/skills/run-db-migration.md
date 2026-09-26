---
title: Run a database migration
description: Apply, verify and roll back migrations on staging and prod.
tags: [tidewater, type:skill, topic:database, topic:deploy]
type: skill
updated: 2026-04-10
---

# Run a database migration

## Rules
File naming and reversibility: [[tidewater/memory/conventions]].

## Steps
1. Staging first: `tidectl migrate --env stg`, then the smoke test.
2. Prod: `tidectl migrate --env prod --dry-run` prints the plan; then without `--dry-run`.
3. Long-running statements (index builds) use `CONCURRENTLY` and run outside peak hours.

## Rollback
- `tidectl migrate --env prod --down 1` applies the paired `.down.sql` of the last migration.
- If the migration dropped or rewrote data, the down file cannot bring it back: restore to the moment before it with [[tidewater/skills/restore-postgres-backup]].
