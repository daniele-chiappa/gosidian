---
title: Code review checklist
description: What a reviewer checks on every pull request.
tags: [global, type:skill]
type: skill
updated: 2026-04-01
---

# Code review checklist

- Tests cover the new behaviour and the failure paths.
- No secrets, no personal data in logs.
- Errors wrapped with context.
- Public functions documented.
- Migrations and feature flags follow the project conventions.
