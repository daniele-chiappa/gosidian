---
title: Restore a Postgres backup
description: Point-in-time recovery of pg-main from WAL-G backups, on staging or prod.
tags: [tidewater, type:skill, topic:database, topic:ops]
type: skill
updated: 2026-03-03
---

# Restore a Postgres backup

## Context
Backups follow [[tidewater/memory/decisions#ADR-007]]: nightly base backup, continuous WAL archiving. WAL archives are retained for 14 days, so the recovery window is two weeks.

## Steps
1. Stop tide-api and tide-worker on the target environment.
2. Pick the target time (UTC) from the incident timeline.
3. `wal-g backup-fetch /var/lib/postgresql/16/main LATEST` on a fresh data directory.
4. Set `recovery_target_time` and `restore_command = 'wal-g wal-fetch %f %p'`, create `recovery.signal`.
5. Start Postgres, wait for "recovery stopping before commit", then `SELECT pg_wal_replay_resume();`.
6. Rebuild `pg-replica` from the restored primary.
7. Start the services, run the smoke test of [[tidewater/skills/deploy-to-staging]].

## Drill
Monthly on staging, see [[tidewater/plans/2026-02-20-backup-pitr]].
