---
title: Add a dbt model
description: Create, test and deploy a new dbt model.
tags: [atlas, type:skill]
type: skill
updated: 2026-06-12
---

# Add a dbt model

1. New model in `models/marts/`, with schema tests.
2. `dbt build --select <model>` against staging.
3. Merge; the next `sales_rollup` run picks it up.
