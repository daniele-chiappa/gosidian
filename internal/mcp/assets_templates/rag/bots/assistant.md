---
title: Documents assistant
type: chat
tags: [{{PROJECT}}]
---

# Documents assistant

The bot of the project: it answers from the documents, with
[[{{PROJECT}}/pipelines/bot]]. The accounts that read the project may
chat here; for a site, see `access` in gosidian's docs (docs/chat.md).

```chat
pipeline: pipelines/bot
title: Documents assistant
welcome: "Ask me about the documents of this project."
access:
  users: true
conversations: {folder: chat, keep: 90d}
limits: {per_minute: 10}
```
