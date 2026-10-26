# gosidian presets

Compose files that start gosidian ready for a purpose, from one `.env`:

```bash
./init.sh                       # .env with the keys and the tokens
$EDITOR .env                    # the models of the rag preset
sudo chown -R 65532:65532 vault state secrets
docker compose -f docker-compose.yml -f docker-compose.rag.yml up -d
docker compose exec gosidian /gosidian plugin accept --pending
```

| File | |
|---|---|
| `docker-compose.yml` | gosidian alone |
| `docker-compose.rag.yml` | answers from a project's documents (docling, images, database on Qdrant, chat) |
| `docker-compose.storage.yml` | remote stores as sources of the IN folders |
| `.env.example` | every setting, with the models' examples |
| `database.yaml` | the database plugin's preset, filled from `.env` |
| `storage.yaml.example` | the sources of the storage plugin |
| `test/smoke.py` | the end-to-end test of base + rag, on a fake model |

The whole walk-through is in [docs/deployment.md](../../docs/deployment.md#presets);
the rag project template's README tells how the documents flow.
