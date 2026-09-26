---
title: INC-3 — Redis out of memory
description: Postmortem of INC-3: the Redis instance hit its memory limit and background jobs stopped.
tags: [tidewater, type:doc, topic:incidents]
type: doc
created: 2026-05-29
---

# INC-3 — Redis out of memory

- **When**: 2026-05-29, 21:15–22:05 UTC
- **Impact**: no ticket PDFs and no confirmation mails for 50 minutes; purchases succeeded, deliveries were late.
- **Root cause**: the page cache and the job streams shared one Redis; a cache stampede after the waiting-room rehearsal filled memory and `jobs.dead` had grown to 1.8 GB.
- **Follow-ups**: IMP-012 (memory alert at 80%) and IMP-013 (trim `jobs.dead`) in [[tidewater/docs/improvements]]; separate cache instance; plan [[tidewater/plans/2026-06-02-inc-3-followups]].
