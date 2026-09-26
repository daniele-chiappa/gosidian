---
title: Rotate the Paylane API keys
description: Replace the Paylane secret key and webhook signing secret without downtime.
tags: [tidewater, type:skill, topic:payments, topic:security]
type: skill
updated: 2026-05-20
---

# Rotate the Paylane API keys

## When
Every 90 days, when a team member with access leaves, or after any suspected exposure.

## Steps
1. Paylane dashboard → Developers → create a new secret key; keep the old one active.
2. `tidectl secrets set PAYLANE_SECRET_KEY --env prod` (and `--env stg` for the sandbox key).
3. Restart tide-api and tide-worker: `tidectl restart api worker --env prod`.
4. Check the charge success rate for 30 minutes on the payments dashboard.
5. Revoke the old key in Paylane after 24 hours.
6. Same flow for the webhook signing secret (`PAYLANE_WEBHOOK_SECRET`).

Record the rotation in [[tidewater/log]].
