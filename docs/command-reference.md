# command reference

```sh
afk --help
afk <command> --help
afk --version
```

The public command surface is intentionally small.

| Command | Behavior |
|---|---|
| `afk add <body...> [--available-at RFC3339] [--request-id ID]` | Append a new `todo` task and print its id. `--available-at` defers claim eligibility. A request id replays the original result for identical actor and inputs. |
| `afk add --dry-run [--json] <body...>` | Validate task shape and metadata without mutating the queue. |
| `afk tasks [--status STATUS] [--stage VALUE] [--json]` | List tasks. Default hides `deleted`; use `--status deleted` or `--status all` when needed. `--stage` filters by pipeline label. |
| `afk task <id> [--json]` | Show one task with metadata, dependencies, events, and attempts. |
| `afk status [--summary] [--blocked] [--json]` | Print queue counts; without `--summary`, also includes `todo` and `doing` task lists plus bounded queue-health signals. `--blocked` adds dependency blocker details. |
| `afk find <query> [--status STATUS] [--json]` | Search id, body, status, cwd, source, tags, resource, agent, group, and error text. |
| `afk take [--dry-run] [--task ID] [--satisfy-gate NAME] [--limit N] [--lease DURATION] [--worker ID] [--json] [--summary] [--full] [--envelope]` | Preview or atomically claim ready work; exact owner claims can satisfy approved gates in the claim transaction. |
| `afk set <id> <status> [note...] [--note TEXT] [--note-file PATH|-] [--stage VALUE] [--worker ID] [--force] [--json] [--summary] [--request-id ID]` | Move a task to `todo`, `doing`, `done`, `failed`, or `deleted`. Carry a claim's worker id into terminal `set`; an unqualified terminal update cannot close a named worker's active attempt. `--force` without `--worker` is the administrative override. `--request-id` cannot be combined with `--summary` or worker-fenced updates. |
| `afk relate <task-id> <related-id> [--type TYPE]` | Record a typed relation between two tasks. |
| `afk gate add <task-id> <name>` | Add a named boolean precondition to a task. |
| `afk gate satisfy <task-id> <name>` | Mark a named precondition satisfied. |
| `afk retry <id> [--disposition manual\|deferred] [--available-at RFC3339] [--reason TEXT] [--json]` | Reopen failed work now, or defer it as `todo` until a required future eligibility time. |
| `afk snapshot [--label LABEL] [--task ID] [--output PATH]` | Export a read-only JSON evidence snapshot for before/after comparisons. |
| `afk checkpoint add|list ...` | Append or list task-scoped progress records with immutable source metadata. |
| `afk artifact add|list ...` | Append or list task-owned artifacts with immutable source metadata. |
| `afk import vybe --source DIR --dry-run\|--apply` | Reconcile or atomically import operational state from a frozen `vybe-archive-v1` export. |
| `afk goal <objective> [--dry-run] [--json] [--cwd PATH] [--setup-command TEMPLATE] [--audit-command TEMPLATE] [--max-tokens N] [--max-iterations N] [--max-duration DURATION] [--token-regex REGEX]` | Compile a free-text objective and atomically queue its group, tasks, events, dependency chain, and durable budgets. |
| `afk goal status <goalID>` | Show a goal group's durable record, member-task counts, limits, cumulative usage, epoch, and limit reason/time. |
| `afk goal resume <goalID> [--max-tokens N] [--max-iterations N] [--max-duration DURATION] [--token-regex REGEX]` | Explicitly change a budget, reset its duration epoch, and atomically requeue budget-limited members. |
| `afk goal audit <taskID> [--audit-command TEMPLATE]` | Run the independent completion auditor on a task; re-queue on disapproval. |
| `afk loop [--command TEMPLATE] [--worker ID] [--lease DURATION] [--timeout DURATION] [--cooldown DURATION] [--heartbeat DURATION] [--max-failures N] [--max-tasks N]` | Autonomous worker-driver: claim ready tasks and run an agent command per task. |
| `afk prompt [--task ID] [--discover] [--output PATH]` | Emit LLM-agent instruction prompts. |
| `afk serve [--addr HOST:PORT]` | Start the local web UI and API. |

## idempotent mutations and Vybe import

`--request-id` uses `(actor, request_id)` as the ledger key. `afk add --agent`
supplies the actor; otherwise the CLI actor is `afk-cli`. The target and
mutation parameters are hashed into the operation identity, so changed input
collides instead of silently replaying another mutation. Successful replay
returns the stored result.

`afk import vybe --dry-run` parses and validates the archive before opening the
transaction, produces the same reconciliation as `--apply`, and rolls back all
task, event, checkpoint, artifact, gate, and receipt writes. `--apply` commits
those writes and a source-SHA receipt together. Re-running an applied source
returns its stored report. Historical telemetry, old receipts, non-task memory,
and records outside active-task history remain in the immutable archive.

## take readiness

`afk take` claims only tasks that are ready. A `todo` task is claimable only when
all hold:

1. every `blocks` relation points to a `done` task
2. no other `doing` task holds the same non-empty resource key
3. it has no unsatisfied gate

`store.Ready` (SQL) is the single authority for readiness.

An owner adapter with an explicit mapping can use `--task <id>`. Repeated
`--satisfy-gate <name>` values are applied in the same transaction before the
readiness predicate; if the task remains blocked, the transaction rolls back.
Retrying with the same `--worker` returns that worker's active claim without
adding a second attempt.

Preview ready tasks without claiming:

```sh
afk take --dry-run --limit 5 --json --full
```

Dry-run JSON previews truncate long task bodies by default unless `--full` is
set. Omit `--full` only when a bounded preview is enough.

Add `--envelope` for a stable object shape. Dry-run output contains
`claimed:false`, `tasks`, and `queue`; claimed output contains `claimed:true`,
`task`, and `queue`.

Use `--summary` with `--dry-run` when you want the ready preview plus queue
counts:

```sh
afk take --dry-run --summary --limit 5
```

## relations

```sh
afk relate "$id" "$other" --type blocks
```

`--type` defaults to `blocks`. Only `blocks` edges gate readiness: a task with a
`blocks` edge to a not-`done` task is not claimable. `relates`, `duplicates`, and
`parent` are informational and never block.

`afk add --blocked-by <id>` is the shorthand for a `blocks` relation:

```sh
afk add --blocked-by 42 "corrected dependent task"
```

`afk add --available-at <RFC3339>` records a UTC eligibility time. Before that
time, the task remains `todo` but is excluded from readiness previews, next-task
claims, and exact claims.

Self-relation is rejected. Re-relating the same pair updates the type.

## gates

```sh
afk gate add "$id" tests-green
afk gate satisfy "$id" tests-green
```

`gate add` adds a named boolean precondition and is idempotent. `gate satisfy`
marks it satisfied and is one-way; satisfying an unknown gate name errors. A task
with any unsatisfied gate is not ready until every gate is satisfied. Neither
subcommand takes flags.

## stage

```sh
afk add --stage triage "review the migration"
afk set "$id" doing --stage in-review
afk tasks --stage triage
```

`--stage` is a free-form human pipeline label (e.g. `triage`, `in-review`),
independent of execution state; it does not affect readiness.
`afk set <id> <status> --stage <value>` sets stage alongside the status change;
omitting `--stage` leaves the existing stage unchanged. `afk tasks --stage <value>`
filters by stage.

## goal

For a step-by-step walkthrough see [Using afk goal](using-goal.md).

`afk goal "<objective>"` compiles a free-text objective into a structured task
contract using a configured setup agent, presents that contract, and — only
after you approve it — queues the contract's tasks as a dependency chain (each
task blocked by the previous one). On approval, `afk goal` prints a
`{"goal_id":"<uuid>","tasks":N}` JSON receipt to stdout:

```sh
afk goal "migrate the auth package off the deprecated session store"
```

The objective is treated as untrusted data and HTML-escaped before it is
interpolated into the agent prompt.

`setup_command` is **empty by default** (fail-closed). With nothing configured,
`afk goal` errors before contacting any agent:

```sh
afk goal "anything"
# Error: no setup command configured (set 'setup_command' in
# ~/.config/afk/goal.yaml or pass --setup-command)
```

Configure it once in `~/.config/afk/goal.yaml` (see configuration.md) or pass
`--setup-command` for a single run. Use `--dry-run` to compile and print the
contract without queueing, or `--json` for the scripted (non-interactive) path:

```sh
afk goal --dry-run --setup-command 'claude -p {{.Prompt}}' "tidy the logging layer"
afk goal --json   --setup-command 'claude -p {{.Prompt}}' "tidy the logging layer"
```

### goal subcommands

```sh
afk goal status <goalID>
afk goal audit <taskID> --audit-command 'claude -p {{.Prompt}}'
```

`goal status` shows the goal group's durable record and a count of its member
tasks by status. The JSON response includes both the raw `objective` (as
originally submitted by the user) and the contract `outcome` (the setup agent's
restatement), plus status, creation time, member-task counts, and a nested
durable `budget` object.
`goal resume` requires at least one explicit change. Resulting nonzero token
and iteration caps must exceed recorded cumulative usage; token accounting
also requires a regex with exactly one decimal capture group.
`goal audit` runs the independent completion auditor on a task; it inspects real
artifacts against the **raw user objective** (not the contract restatement), so
it can catch setup-agent misinterpretation. It emits a terminal
`<approved/>`/`<disapproved/>` marker rather than trusting the completion note.
On disapproval (or any audit error/timeout — disapproval is the fail-safe) the
task is re-queued to `todo`. `audit_command` is also empty by default, so
`goal audit` errors until it is configured.

## loop

For a step-by-step walkthrough see [Using afk loop](using-loop.md).

`afk loop` is the built-in autonomous worker-driver. Each iteration it claims the
first ready task, renders the configured prompt template, runs the configured
agent command, and finalizes the task as `done` or `failed` based on the command
exit status — then repeats:

```sh
afk loop --command 'claude -p {{.Prompt}}' --max-tasks 5
```

`command` is **empty by default** (fail-closed). With nothing configured, the
loop errors immediately:

```sh
afk loop
# Error: no agent command configured (set 'command' in
# ~/.config/afk/loop.yaml or pass --command)
```

Configure it once in `~/.config/afk/loop.yaml` (see configuration.md) or pass
`--command` for a single run. `--max-tasks N` bounds the run to N tasks then
exits cleanly (`0` = unlimited). Flags override the config file per run:

| Flag | Effect |
|---|---|
| `--command TEMPLATE` | Agent command template, e.g. `claude -p {{.Prompt}}`. |
| `--worker ID` | Worker identity for claims (default: `loop-<pid>`). |
| `--lease DURATION` | Exclusive claim duration taken on each task. |
| `--timeout DURATION` | Per-task execution timeout. |
| `--cooldown DURATION` | Pause between ticks when no task is found. |
| `--heartbeat DURATION` | Interval for extending the lease while running; definitive renewal or ownership loss cancels the child process. |
| `--max-failures N` | Halt after this many consecutive task failures. |
| `--max-tasks N` | Exit cleanly after N tasks (`0` = unlimited). |

Loop results are emitted as JSONL on stdout; agent output goes to stderr. See
runner.md for the full driver workflow and how it compares to the external
`afk take`/`afk set` loop.

Grouped invocations are finalized and accounted transactionally by attempt.
The loop preflights expired duration caps after claim and before spawn. When a
token cap is active it streams output unchanged while retaining a bounded 1 MiB
tail per stream, parses the last usable stdout match and then stderr, and fails
closed as `token-usage-unavailable` when usage is absent or overflows.

Non-goal loop finalization is fenced by the worker that claimed the task. If a
heartbeat proves that ownership was lost, or the last confirmed lease expires
while SQLite remains busy, AFK cancels the child and leaves terminal state for
the current owner or stale-work recovery instead of overwriting it.

## status diagnostics

Use `afk status` when you need queue context, not just counts:

```sh
afk status
afk status --json
afk status --blocked
```

Without `--summary`, status output includes active `todo` and `doing` lists.
For `doing` tasks, AFK derives claim diagnostics from persisted timestamps
without changing the queue.

Full text and JSON status also include a `health` section over a fixed 24-hour
window: oldest currently-ready and active ages, stale requeue count, retry
attempt count, and terminal failure rate. Empty age/rate values are `null` in
JSON and `n/a` in text. `stale_requeues` counts the existing `requeued`/`stale`
ledger event, which can represent an expired lease or an age-based unleased
requeue; it is deliberately not labeled as exact lease loss. Retry attempts are
attempts beyond a task's first attempt. `--summary` and `--summary --json`
remain counts-only.

In text output, doing rows include fields such as:

```text
age=31m0s stale=lease_expired
```

In JSON output, doing tasks include a `claim` object when the task has a valid
`started` timestamp:

```json
{
  "id": "1779597305",
  "status": "doing",
  "claim": {
    "age_seconds": 1860,
    "stale": true,
    "reason": "lease_expired"
  }
}
```

`stale` and `reason` are present only when the claim is stale. AFK marks a
claim stale when:

- `lease_expires` is in the past: `reason=lease_expired`
- no lease is set and the task has been `doing` for more than one hour:
  `reason=unleased_age`

Stale diagnostics are read-only. They do not requeue or fail the task. Use
`afk task <id>`, then decide whether to finish, fail, delete, or replace the
work.

`--blocked` explains `todo` tasks held back by unfinished dependencies:

```sh
afk status --blocked --json
```

Example blocked output shape:

```json
{
  "blocked": [
    {
      "task": {"id": "1779597309", "status": "todo"},
      "blockers": [{"id": "1779597305", "status": "todo"}]
    }
  ]
}
```

`--blocked` reports dependency blockers only. Resource-lock blockers are visible
through the active `doing` list and through the stderr explanation from
`afk take` when no task is ready.

Use `--limit 0` to print all currently ready tasks:

```sh
afk take --dry-run --limit 0 --json --full
```

When no task can be claimed, `afk take` keeps stdout empty so worker loops can
test for an empty claim. It writes a short explanation to stderr, including
active resource-lock blockers when they are the reason visible `todo` work is
not ready.

Use `--summary` to claim a task and include queue counts plus
`ready_remaining` after the claim:

```sh
afk take --summary
```

## finalization receipts

`afk set --json` returns the id, new status, a short title derived from the task
body, and the note. Use `--summary` to include queue counts in the same receipt:

```sh
afk set "$id" done --note "verified with go test ./..." --summary
```

Use `--note-file -` for shell-awkward or multi-line evidence:

```sh
printf '%s\n' "$evidence" | afk set "$id" done --note-file - --json
```

## targeted retry

Use `retry <id>` when retrying one specific failed task. This opens a new
attempt by moving the task to `doing`, clears stale task-level error text, and
keeps prior failed attempts in history:

```sh
afk task "$id" --json
afk retry "$id" --reason "fixed the blocker"
# do the work
afk set "$id" done --note "verified" --summary
```

The default `afk retry <id>` disposition is `manual`, equivalent to
`afk set <id> doing --note "retrying: <reason>"`. It opens an attempt now.
Use `afk retry <id> --disposition deferred --available-at <RFC3339>` to return
the task to `todo` without opening an attempt; normal readiness and claim logic
will admit it after the future UTC eligibility time.

`afk set <id> done` and `afk set <id> failed` always leave attempt history
coherent. If there is no open attempt, AFK records a synthetic terminal attempt
so direct manual finalization remains auditable. If a named worker owns the open
attempt, terminal finalization requires that worker id. Use an unqualified
`--force` only for an intentional administrative recovery.

## evidence snapshots

Use `afk snapshot` before and after task work when the verification asks for a
queue-state comparison. Snapshots are read-only JSON and include queue counts,
ready tasks, todo tasks, and doing tasks. Doing task entries include the same
derived claim diagnostics as `afk status --json`.

```sh
afk snapshot --label before --output before.json
afk snapshot --label after --task "$id" --output after.json
```

`--task <id>` adds that task's full record, lifecycle events, and attempts to
the snapshot. Use it for final evidence when a task was claimed, retried,
failed, or manually completed.

## replacement map

| Old behavior | New command |
|---|---|
| `afk ls` | `afk tasks` |
| `afk explain <id>` / `afk show <id>` | `afk task <id>` |
| `afk pop` | `afk take` |
| `afk ready` / `afk run --dry-run` | `afk take --dry-run` |
| `afk done <id>` | `afk set <id> done --note <evidence>` |
| `afk fail <id> <reason>` | `afk set <id> failed --note <reason>` |
| `afk retry <id>` | Still supported. Prefer `afk retry <id> --reason <reason>` for a targeted retry. |
| `afk reset <id>` | `afk set <id> doing --note "retrying"` for a targeted retry, or `afk set <id> todo --note <note>` to return work to the ready queue. |
| `afk prune` / `afk rm` | `afk set <id> deleted` |
| `afk run` | `afk loop` (built-in worker-driver), or an external loop: `afk take`, execute the task, then `afk set`. |

## statuses

Canonical statuses are `todo`, `doing`, `done`, `failed`, and `deleted`.
Old stored values `pending` and `working` are migrated to `todo` and `doing`.

`--stage` is a separate free-form pipeline label, independent of these five
execution states; it does not affect readiness.
