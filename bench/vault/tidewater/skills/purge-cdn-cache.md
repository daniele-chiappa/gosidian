---
title: Purge the CDN cache
description: Invalidate tide-web assets or event images on Nimbus CDN.
tags: [tidewater, type:skill, topic:cdn, topic:frontend]
type: skill
updated: 2026-06-12
---

# Purge the CDN cache

## Steps
1. Single path: `tidectl cdn purge /events/<id>/cover.jpg`.
2. Whole release of tide-web: `tidectl cdn purge --prefix /assets/<build-id>/`.
3. Everything (last resort, origin load spikes): `tidectl cdn purge --all`.

Nimbus propagates in about 60 seconds. During [[tidewater/docs/incidents/2026-06-09-inc-4-cdn-outage|INC-4]] purging did not help: the edge itself was down.
