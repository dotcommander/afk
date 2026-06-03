# command reference

```sh
afk --help
afk <command> --help
afk --version
```

The public command surface is intentionally small.

| Command | Behavior |
|---|---|
| `afk add <body...>` | Append a new `todo` task and print its id. |
| `afk add --dry-run [--json] <body...>` | Validate task shape and metadata without mutating the queue. |
| `afk tasks [--status STATUS] [--stage VALUE] [--json]` | List tasks. Default hides `deleted`; use `--status deleted` or `--status all` when needed. `--stage` filters by pipeline label. |
| `afk task <id> [--json]` | Show one task with metadata, dependencies, events, and attempts. |
| `afk status [--summary] [--blocked] [--json]` | Print queue counts; without `--summary`, also includes `todo` and `doing` task lists. `--blocked` adds dependency blocker details. |
| `afk find <query> [--status STATUS] [--json]` | Search id, body, status, cwd, source, tags, resource, agent, group, and error text. |
| `afk take [--dry-run] [--limit N] [--lease DURATION] [--worker ID] [--json] [--summary] [--full] [--envelope]` | Preview or atomically claim the first ready task. |
| `afk set <id> <status> [note...] [--note TEXT] [--note-file PATH|-] [--stage VALUE] [--json] [--summary]` | Move a task to `todo`, `doing`, `done`, `failed`, or `deleted`. |
| `afk relate <task-id> <related-id> [--type TYPE]` | Record a typed relation between two tasks. |
| `afk gate add <task-id> <name>` | Add a named boolean precondition to a task. |
| `afk gate satisfy <task-id> <name>` | Mark a named precondition satisfied. |
| `afk retry <id> [--reason TEXT] [--json]` | Convenience command for reopening a failed task as `doing` with a new attempt. |
| `afk snapshot [--label LABEL] [--task ID] [--output PATH]` | Export a read-only JSON evidence snapshot for before/after comparisons. |
| `afk goal <objective> [--dry-run] [--json] [--cwd PATH] [--setup-command TEMPLATE] [--audit-command TEMPLATE] [--max-tokens N] [--max-iterations N] [--max-duration DURATION]` | Compile a free-text objective into an approved task contract and queue it as a dependency chain. |
| `afk goal status <goalID>` | Show a goal group's durable record and member-task counts. |
| `afk goal audit <taskID> [--audit-command TEMPLATE]` | Run the independent completion auditor on a task; re-queue on disapproval. |
| `afk loop [--command TEMPLATE] [--worker ID] [--lease DURATION] [--timeout DURATION] [--cooldown DURATION] [--heartbeat DURATION] [--max-failures N] [--max-tasks N]` | Autonomous worker-driver: claim ready tasks and run an agent command per task. |
| `afk prompt [--task ID] [--discover] [--output PATH]` | Emit LLM-agent instruction prompts. |
| `afk serve [--addr HOST:PORT]` | Start the local web UI and API. |

## take readiness

`afk take` claims only tasks that are ready. A `todo` task is claimable only when
all hold:

1. every `blocks` relation points to a `done` task
2. no other `doing` task holds the same non-empty resource key
3. it has no unsatisfied gate

`store.Ready` (SQL) is the single authority for readiness.

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
independent of the five execution states; it does not affect readiness.
`afk set <id> <status> --stage <value>` sets stage alongside the status change;
omitting `--stage` leaves the existing stage unchanged. `afk tasks --stage <value>`
filters by stage.

## goal

`afk goal "<objective>"` compiles a free-text objective into a structured task
contract using a configured setup agent, presents that contract, and — only
after you approve it — queues the contract's tasks as a dependency chain (each
task blocked by the previous one):

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

`goal status` shows the goal group's durable record (objective, status, creation
time) and a count of its member tasks by status. `goal audit` runs the
independent completion auditor on a task; it inspects real artifacts against the
recorded objective and emits a terminal `<approved/>`/`<disapproved/>` marker
rather than trusting the completion note. On disapproval (or any audit
error/timeout — disapproval is the fail-safe) the task is re-queued to `todo`.
`audit_command` is also empty by default, so `goal audit` errors until it is
configured.

## loop

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
| `--heartbeat DURATION` | Interval for extending the lease while running. |
| `--max-failures N` | Halt after this many consecutive task failures. |
| `--max-tasks N` | Exit cleanly after N tasks (`0` = unlimited). |

Loop results are emitted as JSONL on stdout; agent output goes to stderr. See
runner.md for the full driver workflow and how it compares to the external
`afk take`/`afk set` loop.

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

`afk retry <id>` is equivalent to `afk set <id> doing --note "retrying: <reason>"`.
Use `afk set <id> todo --note <note>` instead when you are not retrying it now and
only want to return it to the ready queue.

`afk set <id> done` and `afk set <id> failed` always leave attempt history
coherent. If there is no open attempt, AFK records a synthetic terminal attempt
so direct manual finalization remains auditable.

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
