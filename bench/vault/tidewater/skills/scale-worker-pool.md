---
title: Scale the worker pool
description: Add tide-worker replicas before a high-demand drop and remove them afterwards.
tags: [tidewater, type:skill, topic:ops, topic:scalability]
type: skill
updated: 2026-05-30
---

# Scale the worker pool

## Steps
1. One day before a high-demand drop: `tidectl scale worker=12 --env prod` (default 4).
2. Check the stream lag on `orders.paid` stays under 5 seconds.
3. Enable the waiting room for the drop ([[tidewater/plans/2026-05-04-waiting-room]]).
4. Two hours after the drop: `tidectl scale worker=4 --env prod`.
