---
title: INC-1 — Event pages failing for 25 minutes
description: Postmortem of INC-1: event pages returned errors after a bad cache configuration.
tags: [tidewater, type:doc, topic:incidents]
type: doc
created: 2026-01-30
---

# INC-1 — Event pages failing for 25 minutes

- **When**: 2026-01-30, 18:05–18:30 UTC
- **Impact**: event pages returned 500 for everyone; no purchases possible.
- **Root cause**: a Redis cache key template change deployed without the matching tide-web build.
- **Follow-ups**: deploy tide-api and tide-web together through `tidectl` (done); staging smoke test mandatory ([[tidewater/skills/deploy-to-staging]]).
