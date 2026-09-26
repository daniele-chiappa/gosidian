---
title: Move background jobs to RabbitMQ
description: Evaluate moving tide-worker from Redis Streams to RabbitMQ.
tags: [tidewater, type:plan, status:archived, topic:worker]
type: plan
status: archived
author: Ivo Petrov
created: 2026-03-05
updated: 2026-03-12
---

# Move background jobs to RabbitMQ

## Context
After INC-2 we wondered whether a broker with dead-letter queues would help.

## Result
Consumer groups plus a `jobs.dead` stream give the same guarantees. Not worth a new service. Archived; [[tidewater/memory/decisions#ADR-004]] confirmed.
