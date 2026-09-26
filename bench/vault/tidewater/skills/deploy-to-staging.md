---
title: Deploy to staging
description: Build, publish and roll out a release candidate on kelp-2, then smoke-test it.
tags: [tidewater, type:skill, topic:deploy]
type: skill
updated: 2026-05-30
---

# Deploy to staging

## Trigger
Every release candidate, before any prod rollout ([[tidewater/memory/conventions]]).

## Steps
1. On `drift-1`: `tidectl build --ref <tag>` builds tide-api, tide-worker and tide-web.
2. `tidectl publish --env stg` uploads the build to the registry.
3. Run the migrations with `tidectl migrate --env stg` before switching traffic.
4. `tidectl up --env stg` recreates the services on `kelp-2`.
5. Smoke test: `/healthz` on port 8443, one purchase with the Paylane sandbox, one entry with the Gatekeeper test device.

## Rollback
`tidectl up --env stg --ref <previous-tag>`; migrations follow [[tidewater/skills/run-db-migration]].
