---
title: GraphQL gateway spike
description: Two-day spike on a GraphQL gateway in front of tide-api for the organizer dashboard.
tags: [tidewater, type:plan, status:archived, topic:api, topic:frontend]
type: plan
status: archived
author: Sara Lind
created: 2026-02-12
updated: 2026-02-16
---

# GraphQL gateway spike

## Context
The organizer dashboard fetches many small resources. Could a gateway help?

## Result
The gateway doubled the moving parts and broke the CDN caching of event pages. Batch endpoints on the REST API solve the dashboard case. Abandoned; [[tidewater/memory/decisions#ADR-003]] stays as is.
