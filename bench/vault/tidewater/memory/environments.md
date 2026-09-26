---
title: Tidewater Environments
description: Hosts, ports and where credentials live for staging and prod.
tags: [tidewater, type:memory, topic:ops]
type: memory
importance: 4
updated: 2026-06-05
---

# Environments

## Staging

- URL: https://stg.tidewater.example
- Host `kelp-2`; the staging API listens on port 8443.
- Database: a nightly copy of prod with personal data scrubbed.

## Prod

- URL: https://tidewater.example, HAProxy on `kelp-1` terminating TLS.
- tide-api and tide-worker on `kelp-1` and `kelp-3`.
- `pg-main` (primary) on `kelp-3`, port 5433.
- `pg-replica` (read replica) on `kelp-4`, port 5434. Read-only consumers such as reporting use the replica, never the primary.
- Redis on `kelp-3`, MinIO on `kelp-4`.

## Build and release

- Build runner `drift-1` builds and publishes every release.
- Image registry: `registry.tidewater.example`. The deploy token for it lives in `.env.deploy` on `drift-1`, readable only by the `ci` user; it is never committed.

## Secrets

- Runtime secrets (Paylane, MailRiver, database) in `.env.prod` on each host, managed with `tidectl secrets`.
- Rotation of the Paylane keys: [[tidewater/skills/rotate-paylane-keys]].
