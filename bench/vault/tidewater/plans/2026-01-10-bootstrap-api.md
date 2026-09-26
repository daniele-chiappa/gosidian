---
title: Bootstrap tide-api
description: First version of the Go API with events, orders and health checks.
tags: [tidewater, type:plan, status:done, topic:api]
type: plan
status: done
author: Ivo Petrov
created: 2026-01-10
updated: 2026-01-19
---

# Bootstrap tide-api

## Context
We need a first API to build the purchase path on. Language chosen in [[tidewater/memory/decisions#ADR-001]].

## Solution
- Module layout: `cmd/tide-api`, `internal/events`, `internal/orders`, `internal/http`.
- OpenAPI spec first, handlers generated with oapi-codegen.
- `/healthz` and `/readyz` for HAProxy.

## Outcome
Shipped in v1.0.0 on 2026-01-19. Postgres migrations from day one, see [[tidewater/memory/conventions]].
