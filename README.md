# afk

`afk` is a local SQLite task queue for coding agents. The CLI is organized like a small task API:

```sh
id=$(afk add "fix the failing queue test")
afk tasks
afk task "$id"
afk take --dry-run --limit 5 --json --full
afk status --blocked
afk take --lease 30m --worker codex:1 --summary
afk set "$id" done --note "verified" --worker codex:1 --summary
```

Tasks move through `todo`, `doing`, `done`, `failed`, `deleted`, and the
goal-only suspension state `budget-limited`.
Readiness is narrower than `todo`: a task is claimable only when every
`blocks` relation is `done`, no other active `doing` task holds its resource
key, no gate is unsatisfied, and its optional `available_at` time has arrived.
Full `afk status` includes a bounded 24-hour health summary, terminal-attempt
duration p50/p90, and active task details. `afk status --blocked` explains
dependency blockers; `doing` task status output also shows claim age and stale
lease diagnostics. Counts-only `afk status --summary` keeps its compact
contract.

`--stage` is a free-form human pipeline state (e.g. `triage`, `in-review`),
independent of execution state; it does not affect readiness.

## install

Requires Go 1.26+.

```sh
go install github.com/dotcommander/afk/cmd/afk@latest
```

Or build from source:

```sh
git clone https://github.com/dotcommander/afk
cd afk
go build -o afk ./cmd/afk
```

## core commands

| Command | Purpose |
|---|---|
| `afk add <body...> [--available-at RFC3339] [--stage VALUE] [--request-id ID]` | Add a task. `--available-at` defers claim eligibility without changing `todo` status. Use `--dry-run --json` to validate without writing. A request id replays the original result for the same actor and inputs. |
| `afk tasks [--status STATUS] [--stage VALUE] [--json]` | List tasks. Deleted tasks are hidden unless requested. `--stage` filters by pipeline state. |
| `afk task <id> [--json]` | Show one full task with events and attempts. |
| `afk status [--summary] [--blocked] [--json]` | Get queue counts. Full status also includes active task lists and 24-hour queue-health signals; `--blocked` explains dependency-blocked todo tasks. |
| `afk find <query> [--json]` | Search task text and metadata for duplicate checks. |
| `afk take [--dry-run] [--task ID] [--satisfy-gate NAME] [--lease DURATION] [--worker ID] [--summary] [--full] [--envelope]` | Preview or claim ready work; exact owner claims can satisfy approved gates atomically. |
| `afk set <id> <status> [note...] [--note TEXT] [--note-file PATH|-] [--stage VALUE] [--worker ID] [--force] [--json] [--summary] [--request-id ID]` | Set `todo`, `doing`, `done`, `failed`, or `deleted`. A named worker owns its active attempt; carry the same worker id from `take` into terminal `set`. `--force` without `--worker` is the administrative override. Request-id replay is incompatible with `--summary` and worker-fenced updates. |
| `afk retry <id> [--disposition manual\|deferred] [--available-at RFC3339] [--reason TEXT] [--json]` | Open an attempt now (`manual`, default) or return failed work to `todo` until a required future eligibility time (`deferred`). |
| `afk relate <task-id> <related-id> [--type blocks\|relates\|duplicates\|parent]` | Record a typed relation between tasks. Defaults to `blocks`. Only `blocks` edges gate readiness; `relates`/`duplicates`/`parent` are informational. |
| `afk gate add <id> <name>` / `afk gate satisfy <id> <name>` | Named boolean preconditions. A task with any unsatisfied gate is not claimable until every gate is satisfied. Satisfy is one-way. |
| `afk snapshot [--label LABEL] [--task ID] [--output PATH]` | Export read-only JSON evidence, including queue health, for before/after comparisons. |
| `afk checkpoint add|list ...` | Append or list task-scoped progress records with immutable provenance. |
| `afk artifact add|list ...` | Append or list task-owned artifact records with immutable provenance. |
| `afk import vybe --source DIR --dry-run|--apply` | Reconcile or atomically import operational state from a frozen `vybe-archive-v1` export. |
| `afk goal <objective> [--dry-run] [--json] [--cwd PATH]` | Compile a free-text objective into an approved task contract and atomically queue it with durable budgets. See below. |
| `afk goal status <goalID>` / `afk goal resume <goalID> ...` | Inspect durable usage or explicitly raise/change a limited goal's budget and requeue its suspended tasks. |
| `afk prompt [--task ID]` | Generate LLM-agent instructions. |
| `afk serve` | Run the web visibility layer. |

`--request-id` is keyed by actor plus request id. `afk add --agent NAME` uses
`NAME` as the actor; otherwise CLI mutations use `afk-cli`. Reusing a key with
different targets or parameters fails as a collision. A replay returns the
stored mutation result rather than re-reading mutable task state.

Useful triage sequence when workers claim nothing:

```sh
afk status --blocked
afk tasks --status doing --json
afk take --dry-run --limit 0 --json --full
```

## goal

`afk goal "<objective>"` compiles a free-text objective into a structured task
contract using a configured setup agent, presents that contract, and — only
after you approve it — queues the contract's tasks as a dependency chain (each
task blocked by the previous one).

```sh
afk goal "migrate the auth package off the deprecated session store"
```

The objective is treated as untrusted data and HTML-escaped before it is
interpolated into the agent prompt, so it cannot inject instructions into the
surrounding prompt structure.

| Flag | Effect |
|---|---|
| `--dry-run` | Compile and print the contract, then exit without queueing anything. |
| `--json` | Print the contract as JSON and skip the interactive approval prompt (scripted, non-interactive path — no tasks are queued). |
| `--cwd PATH` | Working directory recorded on the queued tasks (default: current directory). |
| `--setup-command TEMPLATE` | Override the setup agent command for contract compilation. |
| `--audit-command TEMPLATE` | Override the independent auditor command. |
| `--max-tokens N` | Per-goal token budget (`0` = unlimited). |
| `--max-iterations N` | Per-goal iteration cap (`0` = unlimited). |
| `--max-duration DURATION` | Per-goal wall-clock cap (`0` = unlimited). |
| `--token-regex REGEX` | Regex with exactly one decimal capture group for token usage; required when `--max-tokens` is nonzero. |

Without `--dry-run`/`--json`, `afk goal` prints the compiled contract and prompts
`Approve contract? [yes/no]:` on stderr. Only an explicit `yes` queues the tasks;
anything else declines and writes nothing. On `yes`, it also prints a JSON receipt
line `{"goal_id":"<uuid>","tasks":N}` to stdout so you can capture the goal ID for
use with `afk goal status` or `afk goal audit`.

### subcommands

- `afk goal status <goalID>` — show a goal group's durable record and a count of
  its member tasks by status. The JSON response includes the raw `objective` (as
  originally submitted), the contract `outcome` (the setup agent's restatement),
  the goal status, creation time, member-task counts, and a nested `budget`
  object with limits, cumulative usage, current duration epoch, and limit reason/time.
- `afk goal resume <goalID> [--max-tokens N] [--max-iterations N]
  [--max-duration D] [--token-regex REGEX]` — require at least one explicit
  change, validate the resulting budget, reset the duration epoch, and requeue
  every `budget-limited` member atomically. Nonzero cumulative caps must exceed
  already-recorded usage.
- `afk goal audit <taskID> [--audit-command TEMPLATE]` — run the independent
  completion auditor on a task. The auditor is a separate agent invocation that
  inspects the real artifacts against the **raw user objective** (not the
  contract restatement), so it can catch setup-agent misinterpretation. It emits
  a terminal `<approved/>`/`<disapproved/>` marker and does not trust the task's
  completion note. On disapproval (or any audit error/timeout — disapproval is
  the fail-safe default) the task is re-queued to `todo`.

### configuration

Goal settings live in `~/.config/afk/goal.yaml`, written with defaults on first
run. `setup_command` and `audit_command` are **empty by default** — the workflow
is fail-closed: `afk goal` errors until you configure a setup agent command
(via the file or `--setup-command`), and `goal audit` errors until an audit
command is configured. The file also holds the setup/audit prompt templates and
the budget caps.

Goal creation stores limits with the group, and `afk loop` accounts them in
SQLite by task attempt so restarts and concurrent workers cannot double-count.
Duration begins at the first invocation in the current epoch; resume starts a
new duration epoch while token and iteration usage remain cumulative. When a
token cap is configured, missing or overflowing usage fails closed with
`token-usage-unavailable` and suspends remaining work. Agent output is still
streamed unchanged; accounting inspects only a bounded 1 MiB tail per stream,
using the last parseable stdout match before falling back to stderr.

## removed command replacements

- `ls` -> `tasks`
- `explain` / `show` -> `task <id>`
- `pop` -> `take`
- `ready` / `run --dry-run` -> `take --dry-run`
- `done` -> `set <id> done --note <evidence>`
- `fail` -> `set <id> failed --note <reason>`
- `retry` -> `retry <id> --reason <reason>`
- `reset` -> `set <id> doing "retrying"` for targeted retry, or `set <id> todo <note>` to return work to the ready queue
- `prune` / `rm` -> `set <id> deleted`
- `run` -> a shell or agent loop that calls `take`, executes the task, then calls `set`

Example worker loop:

```sh
worker="$USER:$$"
task_json=$(afk take --lease 30m --worker "$worker" --summary)
test -n "$task_json" || exit 0
id=$(printf '%s\n' "$task_json" | jq -r .task.id)
body=$(printf '%s\n' "$task_json" | jq -r .task.body)

if agent-command "$body"; then
  afk set "$id" done --note "agent-command completed" --worker "$worker" --summary
else
  afk set "$id" failed --note "agent-command failed" --worker "$worker" --summary
fi
```

## docs

- [`docs/command-reference.md`](docs/command-reference.md)
- [`docs/faq.md`](docs/faq.md)
- [`docs/getting-started.md`](docs/getting-started.md)
- [`docs/tasks.md`](docs/tasks.md)
- [`docs/scheduling.md`](docs/scheduling.md)
- [`docs/workers.md`](docs/workers.md)
- [`docs/configuration.md`](docs/configuration.md)
- [`docs/using-goal.md`](docs/using-goal.md) — step-by-step guide to `afk goal`
- [`docs/using-loop.md`](docs/using-loop.md) — step-by-step guide to `afk loop`

## license

MIT — see [LICENSE](LICENSE).
