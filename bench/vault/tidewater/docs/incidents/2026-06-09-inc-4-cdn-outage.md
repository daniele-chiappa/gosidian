---
title: INC-4 — Nimbus CDN edge outage
description: Postmortem of INC-4: the CDN edge in the EU region failed and tide-web did not load.
tags: [tidewater, type:doc, topic:incidents]
type: doc
created: 2026-06-09
---

# INC-4 — Nimbus CDN edge outage

- **When**: 2026-06-09, 07:50–08:35 UTC
- **Impact**: tide-web assets and event images did not load in Europe; the API was fine, Gatekeeper kept working.
- **Root cause**: Nimbus CDN outage in its EU edge; nothing to fix on our side.
- **Follow-ups**: fallback origin for `/assets/` served by HAProxy when the CDN health check fails (open); status page for organizers.
