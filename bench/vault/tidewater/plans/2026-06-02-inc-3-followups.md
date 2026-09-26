---
title: INC-3 follow-ups
description: Redis memory limits, alerting and eviction policy after the Redis out-of-memory incident.
tags: [tidewater, type:plan, status:in-progress, topic:ops, topic:redis]
type: plan
status: in-progress
author: Luca Ferri
created: 2026-06-02
updated: 2026-06-18
---

# INC-3 follow-ups

## Context
[[tidewater/docs/incidents/2026-05-29-inc-3-redis-oom|INC-3]].

## Tasks
- [x] `maxmemory` 6 GB and `noeviction` on the streams instance
- [x] Separate Redis instance for the page cache with `allkeys-lru`
- [ ] Alert at 80% memory (IMP-012 in [[tidewater/docs/improvements]])
- [ ] Trim policy for `jobs.dead`
