---
title: Real-time sales feed
description: Feed the organizer dashboard with sales at most five minutes old.
tags: [atlas, type:plan, status:in-progress]
type: plan
status: in-progress
author: Nadia Osei
created: 2026-05-20
---

# Real-time sales feed

## Solution
- Consume `orders.paid` from tidewater's Redis Streams with a dedicated consumer group.
- Materialized view in ClickHouse per event and minute.

## Status
Consumer running on staging; waiting for the INC-3 Redis changes in prod.
