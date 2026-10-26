# The fitness pilot of the agent teams

A small, repeatable benchmark of a [team of agents](../../docs/agents.md)
at work on one objective (IMP-195 phase 1c): from a fixed corpus of open
documents, a team writes the knowledge of beginners' training into the
vault, a workout plan for each of five invented people, and a little web
app that shows them. The same objective, people, questions and checks run
again with another team (other models, other executors, other limits),
and the numbers say how each did.

The team works in Italian, the language of the maintainers' lab: the
objective, the people, the questions and the checks are in Italian.

## Layout

| Path | What |
|---|---|
| `corpus.json`, `download.py` | The corpus: the WHO 2020 guidelines on physical activity and sedentary behaviour and nine open-access reviews and studies from PubMed Central, with their licences, SHA-256 and where they come from. `download.py DIR` fetches and checks them (standard library). |
| `persone.json` | The five people: age, health, habits, goal, time, equipment. Invented. |
| `obiettivo.md` | The objective the team's coordinator receives: what to build and how it is accepted. |
| `ruoli/` | The agent note of each role (its system prompt): `coordinatore`, `conoscenza`, `sviluppo`, `revisore`. |
| `squadre/` | The ```team block of each team that ran the pilot. |
| `domande.jsonl` | 20 questions on the corpus with the expected answer, the source and the passage that holds it. |
| `verifiche.json` | For each person, the checks a good plan meets (safety, progression, the guidelines, fitting the person, sources), each with its source. |
| `pipelines/` | The benchmark's pipelines: `converti-fonte` (a PDF into a note, with docling), `rispondi` and `giudica` (the questions), `giudica-scheda` (the plans), `prova-app` (the app). |
| `pilota.py` | Sets the project up, gives the objective, follows the team, measures (standard library). |
| `results/` | One JSON per run of the pilot. |

## What you need

- a gosidian server with pipelines on, an MCP token that writes the
  project, and the plugins: `claude` (or the executor of the team you
  try), `exec` and `preview` (the checks and the preview of the app),
  `notify` (the approvals), `docling` (the corpus);
- a git repository for the app, reachable by the executor's preset, with
  pull requests: the maintainers' lab uses `agents-fitness` on Gitea, its
  `main` protected, a skeleton with `npm test` and `npm run build` and no
  dependencies;
- an LLM endpoint for `llm.chat` with the two judges (the lab:
  `gemma4:12b` and `qwen3:14b` on Ollama, a context of 12,288 tokens).

## Steps

```bash
export GOSIDIAN_MCP_URL=https://your-server/mcp
T=token.txt                                       # a file with the MCP token

python3 download.py corpus                        # 10 PDFs, 14 MB
# a project "fitness" with pipelines and the IN folder on (Projects window)
python3 pilota.py --token-file $T setup --team squadre/claude-sonnet.yaml
python3 pilota.py --token-file $T fonti corpus    # into fonti/, with docling
python3 pilota.py --token-file $T via             # the objective, to the coordinator
python3 pilota.py --token-file $T stato           # while the team works
python3 pilota.py --token-file $T misura --variant claude-sonnet --reference
python3 pilota.py report results/claude-sonnet.json
```

The team asks a person before each merge of the app (`approve: merge`,
through `notify`): those answers are part of what the pilot counts.

To run the pilot again in the same server, move the last run's notes out
of the project first (`conoscenza/`, `misure/`, `tasks/`: a run's token
reads only its project, so another project keeps them for comparison),
give the app a repository started again from the skeleton, run `setup`
again (it puts the team's block back as the file says) and measure under
another `--variant`. `misura` counts only the tasks of the project and the
runs started after the objective.

## Results

| Run | Team | Knowledge (team's notes / sources, two judges) | Plans | App | Cost |
|---|---|---|---|---|---|
| [2026-10-25](results/claude-sonnet.md) | Claude Code, sonnet in every role | 2.9–5.1 / 7.6–7.8 | 39–39.5 of 40 | 24/24 steps, computer and phone | 10.67 USD (list prices), 95 min, 2 approvals |
| [2026-10-25, second](results/claude-sonnet-giro2.md) | the same, on gosidian v2.116.0 | 0.75–4.15 / 7.6–7.8 | 34.5–37 of 40 | 24/24 steps, computer and phone | 10.35 USD counted + 6.2 M tokens of two failed steps, 127 min, 1 approval |
| [2026-10-25, third](results/claude-sonnet-giro3.md) | the same, on gosidian v2.117.0 | 2.0–3.0 / 7.6–7.8 | 33–35 of 40 | 24/24 steps, computer and phone | 8.98 USD and 29.3 M tokens counted (15.6 M in failed steps, unpriced), 105 min, 2 approvals |
| [2026-10-25, fourth](results/claude-sonnet-giro4.md) | the same, on gosidian v2.118.0 | 4.4–6.0 / 7.6–7.8 | 38–39 of 40 | 24/24 steps, computer and phone | 12.99 USD and 21.6 M tokens counted, 82 min of work (587 with a night's wait), 5 approvals; closed by the coordinator |

## The measures

- **Knowledge.** A fixed agent (Claude Code with haiku, read only, no
  MCP) answers the 20 questions reading only the team's notes
  (`conoscenza/`); two local judges score each answer from -10 to 10
  against the expected answer and its passage, with the rules of the
  [RAG benchmarks](../../docs/rag-benchmarks.md). With `--reference` the
  same agent answers from the sources (`fonti/`): the ceiling.
- **Plans.** The same judges check each person's plan against its checks:
  yes (1), in part (0.5), no (0). A missing plan scores 0.
- **App.** `main` built and tested, published as a preview and used as a
  person would, on a computer and on a phone (390 px): the five people by
  name, each plan, the exercises; errors in the console, failed requests.
- **Cost.** The team's tasks, attempts, cost (at the API's list prices:
  with a subscription Claude Code still reports it), tokens, the time from
  the objective to its close, and the people's answers.
