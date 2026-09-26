---
title: Ops on-call
description: Role for incidents, alerts and infrastructure changes.
tags: [tidewater, type:agent]
type: agent
updated: 2026-06-01
---

# Ops on-call

## Rotation (2026)
- April: Ivo Petrov
- May: Marta Ruiz
- June: Luca Ferri (lead), Ivo Petrov as backup
- July: Sara Lind

## First moves on an alert
1. Acknowledge in the pager within 5 minutes.
2. Check [[tidewater/memory/environments]] for hosts, then the dashboards.
3. Payments alerts: [[tidewater/skills/investigate-double-charge]].
4. Anything customer-facing longer than 15 minutes becomes an incident with a postmortem ([[tidewater/docs/incidents/README]]).
