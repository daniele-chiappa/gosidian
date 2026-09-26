---
title: ClickHouse cost review
description: Reduce storage by tiering partitions older than six months.
tags: [atlas, type:plan, status:draft]
type: plan
status: draft
author: Nadia Osei
created: 2026-06-10
---

# ClickHouse cost review

## Idea
- Move partitions older than six months to object storage.
- Drop raw ingestion tables after 90 days, keep aggregates.
