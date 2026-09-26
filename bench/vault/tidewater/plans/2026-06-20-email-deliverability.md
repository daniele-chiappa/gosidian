---
title: Email deliverability
description: Reduce bounces and spam placement of MailRiver messages.
tags: [tidewater, type:plan, status:draft, topic:email]
type: plan
status: draft
author: Nadia Osei
created: 2026-06-20
updated: 2026-06-20
---

# Email deliverability

## Context
4% of order confirmations bounce; Outlook places some in junk.

## Ideas
- Dedicated sending domain with its own DKIM key.
- Bounce webhook from MailRiver marks the buyer's address as undeliverable.
- Plain-text part for every template.
