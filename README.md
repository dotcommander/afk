# afk

`afk` is a local SQLite task queue for coding agents. The CLI is organized like a small task API:

```sh
id=$(afk add "fix the failing queue test")
afk tasks
afk task "$id"
afk take --dry-run --limit 5 --json --full
afk status --blocked
afk take --lease 30m --worker codex:1 --summary
afk set "$id" done --note "verified" --summary
```

Tasks move through `todo`, `doing`, `done`, `failed`, and `deleted`.
Readiness is narrower than `todo`: a task is claimable only when every
`blocks` relation is `done`, no other active `doing` task holds its resource
key, and no gate is unsatisfied.
`afk status --blocked` explains dependency blockers; `doing` task status output
also shows claim age and stale lease diagnostics.

`--stage` is a free-form human pipeline state (e.g. `triage`, `in-review`),
independent of the five execution states; it does not affect readiness.

## core commands

| Command | Purpose |
|---|---|
| `afk add <body...> [--stage VALUE]` | Add a task. Use `--dry-run --json` to validate without writing. |
| `afk tasks [--status STATUS] [--stage VALUE] [--json]` | List tasks. Deleted tasks are hidden unless requested. `--stage` filters by pipeline state. |
| `afk task <id> [--json]` | Show one full task with events and attempts. |
| `afk status [--summary] [--blocked] [--json]` | Get queue counts, plus active task lists by default. `--blocked` explains dependency-blocked todo tasks. |
| `afk find <query> [--json]` | Search task text and metadata for duplicate checks. |
| `afk take [--dry-run] [--lease DURATION] [--worker ID] [--summary] [--full] [--envelope]` | Preview or claim ready work. |
| `afk set <id> <status> [note...] [--note TEXT] [--note-file PATH|-] [--stage VALUE] [--json] [--summary]` | Set `todo`, `doing`, `done`, `failed`, or `deleted`. |
| `afk retry <id> [--reason TEXT] [--json]` | Open a new attempt for a failed task. |
| `afk relate <task-id> <related-id> [--type blocks\|relates\|duplicates\|parent]` | Record a typed relation between tasks. Defaults to `blocks`. Only `blocks` edges gate readiness; `relates`/`duplicates`/`parent` are informational. |
| `afk gate add <id> <name>` / `afk gate satisfy <id> <name>` | Named boolean preconditions. A task with any unsatisfied gate is not claimable until every gate is satisfied. Satisfy is one-way. |
| `afk snapshot [--label LABEL] [--task ID] [--output PATH]` | Export read-only JSON evidence for before/after comparisons. |
| `afk goal <objective> [--dry-run] [--json] [--cwd PATH]` | Compile a free-text objective into an approved task contract and queue it. See below. |
| `afk prompt [--task ID]` | Generate LLM-agent instructions. |
| `afk serve` | Run the web visibility layer. |

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

Without `--dry-run`/`--json`, `afk goal` prints the compiled contract and prompts
`Approve contract? [yes/no]:` on stderr. Only an explicit `yes` queues the tasks;
anything else declines and writes nothing.

### subcommands

- `afk goal status <goalID>` — show a goal group's durable record (objective,
  status, creation time) and a count of its member tasks by status.
- `afk goal audit <taskID> [--audit-command TEMPLATE]` — run the independent
  completion auditor on a task. The auditor is a separate agent invocation that
  inspects the real artifacts against the goal's recorded objective and emits a
  terminal `<approved/>`/`<disapproved/>` marker; it does not trust the task's
  completion note. On disapproval (or any audit error/timeout — disapproval is
  the fail-safe default) the task is re-queued to `todo`.

### configuration

Goal settings live in `~/.config/afk/goal.yaml`, written with defaults on first
run. `setup_command` and `audit_command` are **empty by default** — the workflow
is fail-closed: `afk goal` errors until you configure a setup agent command
(via the file or `--setup-command`), and `goal audit` errors until an audit
command is configured. The file also holds the setup/audit prompt templates and
the budget caps.

The budget caps enforced from the configured loop are the iteration and
wall-clock limits; the token cap (`--max-tokens` / `max_tokens`) is only enforced
when the agent emits a token count that a configured `token_regex` can parse —
otherwise the recorded token usage stays `0` and the token cap does not trip.

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
task_json=$(afk take --lease 30m --worker "$USER:$$" --summary)
test -n "$task_json" || exit 0
id=$(printf '%s\n' "$task_json" | jq -r .task.id)
body=$(printf '%s\n' "$task_json" | jq -r .task.body)

if agent-command "$body"; then
  afk set "$id" done --note "agent-command completed" --summary
else
  afk set "$id" failed --note "agent-command failed" --summary
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
