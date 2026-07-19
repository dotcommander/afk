# FAQ and troubleshooting

```sh
afk status --summary
afk status --blocked
afk take --dry-run --limit 5 --json --full
afk tasks --status doing --json
afk tasks --status failed --json
```

Run those commands first when the queue looks surprising. They answer the usual
questions: how much work exists, which dependencies are blocking todo work, what
can be claimed, what is currently holding resources, and what failed recently.

## Quick diagnosis

| Symptom | First commands | Likely cause |
|---|---|---|
| `afk take` prints no task | `afk take --dry-run --limit 0 --json --full`, `afk status --blocked`, and `afk tasks --status doing --json` | No ready work, unfinished dependencies, or active resource locks. |
| `afk status` shows `todo`, but workers claim nothing | `afk status --blocked` and `afk tasks --status doing --json` | `todo` is not the same as ready. Dependencies or resource locks can block it. |
| A failed task still looks failed after retry | `afk task <id> --json` | Retry must start with `afk retry <id> --reason "..."`; direct `done` is allowed but may not match the intended retry story. |
| Deleted work disappeared from lists | `afk tasks --status deleted` | Default task lists hide `deleted`. History is still available. |
| You need a before/after queue comparison | `afk snapshot --label before --output before.json` | Use snapshots instead of manually reconstructing old queue state. |
| Agents keep duplicating work | `afk find <repo-or-topic> --json` | Discovery or add flow skipped duplicate checks. |
| A queue path seems wrong | `afk --queue /path/to/tasks.sqlite status` and `printf '%s\n' "$AFK_QUEUE"` | The command is using the default queue, `AFK_QUEUE`, or a normalized `.sqlite` sibling path. |

## Command basics

### What is AFK for?

AFK is a local SQLite task queue for coding agents. It stores durable task state,
not process supervision. A worker or agent claims one ready task, does the work,
and records the outcome.

The lifecycle is:

```text
todo -> doing -> done | failed | deleted
```

Readiness is separate from status. A task can be `todo` but not ready when a
dependency is unfinished or another `doing` task owns the same non-empty
resource key.

### What commands should I use day to day?

```sh
afk add "fix the failing queue test"
afk tasks
afk task "$id"
afk find queue --json
afk take --dry-run --limit 5 --json --full
afk take --lease 30m --worker codex:1 --summary
afk set "$id" done --note "verified" --worker codex:1 --summary
afk set "$id" failed --note "missing credentials" --worker codex:1 --summary
afk set "$id" deleted --note "superseded"
afk snapshot --label after --task "$id"
```

Use `afk <command> --help` for exact flags.

### Which old commands are gone?

Use these replacements:

| Old command | Current command |
|---|---|
| `afk ls` | `afk tasks` |
| `afk show <id>` or `afk explain <id>` | `afk task <id>` |
| `afk pop` | `afk take` |
| `afk ready` or `afk run --dry-run` | `afk take --dry-run` |
| `afk done <id>` | `afk set <id> done --note <evidence>` |
| `afk fail <id> <reason>` | `afk set <id> failed --note <reason>` |
| `afk retry <id>` | Still supported. Prefer `afk retry <id> --reason <reason>` for a targeted retry. |
| `afk reset <id>` | `afk set <id> doing --note "retrying"` for a targeted retry, or `afk set <id> todo --note <note>` to return work to the queue. |
| `afk prune` or `afk rm` | `afk set <id> deleted` |
| `afk run` | An external loop that calls `take`, executes the task, then calls `set`. |

## Queue paths and configuration

### Where is the queue stored?

By default:

```text
~/.claude/queue/tasks.sqlite
```

Override it per command:

```sh
afk --queue /tmp/tasks.sqlite status
```

Or through the environment:

```sh
AFK_QUEUE=/tmp/tasks.sqlite afk tasks
```

Non-`.sqlite` paths are normalized to a sibling `.sqlite` database. For example,
`/tmp/tasks.jsonl` becomes `/tmp/tasks.sqlite`.

### How do I know which queue an agent used?

Inspect the command the agent ran, its environment, and the task metadata:

```sh
printf '%s\n' "$AFK_QUEUE"
afk status --summary
afk tasks --status all --json
```

If you suspect a different queue, run the same inspection with an explicit path:

```sh
afk --queue /path/to/tasks.sqlite status --summary
afk --queue /path/to/tasks.sqlite tasks --status all --json
```

### Why did `/tmp/tasks.jsonl` create `/tmp/tasks.sqlite`?

AFK stores data in SQLite. A non-`.sqlite` queue path is accepted for convenience
but normalized to a `.sqlite` sibling path.

## Adding and validating tasks

### How do I add a good task?

Give the worker everything needed to execute without conversation context:

```sh
afk add \
  --cwd /path/to/project/code/project \
  --source task-discovery \
  --tag discovery \
  --resource repo:/path/to/project/code/project \
  "Fix settings persistence. Evidence: /path/to/project/code/project/internal/settings/store.go:42 drops the save error. Scope: internal/settings only. Success: settings survive refresh. Verify with go test ./internal/settings. Reject-if: settings persistence moved out of this package."
```

A strong task body includes:

- Evidence: current file, command output, queue record, failing behavior, or docs/source mismatch.
- Scope: exact package, command, document, or behavior to touch.
- Success: observable done state.
- Verify with: exact local command or deterministic check.
- Reject-if: condition that makes the task invalid or blocked.

`--stage <value>` is an optional free-form pipeline label (such as `triage` or
`in-review`) independent of status; it does not affect readiness.

### How do I validate before writing?

Use dry-run:

```sh
afk add --dry-run --json "validate this task shape"
```

Use `--diagnose` when you want detailed validation failures:

```sh
afk add --dry-run --diagnose "too vague"
```

Dry-run validates shape and options. It does not prove the task is valuable or
that the referenced files still contain the problem.

### Why did `afk add` reject my task?

Common causes:

- The body is too vague to execute.
- Metadata is invalid, such as an unsupported priority.
- `--blocked-by` references a missing task.
- The dependency would create a cycle.
- `--force` and `--diagnose` were used together.

Run:

```sh
afk add --dry-run --diagnose <your task body>
```

Then make the task more specific instead of forcing it unless you are importing
trusted, already-reviewed work.

### How should I avoid duplicate tasks?

Search before adding:

```sh
afk find "settings persistence" --json
afk find "repo:/path/to/project/code/project" --json
afk tasks --status todo --json
afk tasks --status doing --json
```

Discovery workflows should reject duplicates already visible in `todo` or
`doing`. Failed tasks can be retried or used as evidence, but they should not be
blindly duplicated.

## Claiming and readiness

### Why does `afk take` produce no output?

An empty stdout is normal when no task can be claimed. This is deliberate so
worker loops can test for an empty claim.

Diagnose readiness:

```sh
afk take --dry-run --limit 0 --json --full
afk status
afk tasks --status todo --json
afk tasks --status doing --json
```

If visible `todo` work is held back by a resource lock, an unsatisfied gate, or
a `blocks` relation pointing at unfinished work, `afk take` keeps stdout empty
and writes a short explanation to stderr.

### Why does `afk status` show todo work but `afk take` claims nothing?

`todo` means unfinished. Ready means claimable.

A `todo` task is not ready while:

- a `blocks` relation points at a task that is not `done`
- another `doing` task has the same non-empty resource key
- a gate on the task is unsatisfied

`store.Ready` (SQL) is the single authority for this. A task is claimable only
when every `blocks` relation points to a `done` task, no other `doing` task
holds its resource key, and no gate is unsatisfied.

Inspect the task and active work:

```sh
afk task "$id"
afk status --blocked
afk tasks --status doing --json
afk take --dry-run --limit 0 --json --full
```

Read those commands this way:

- `afk status --blocked` shows `todo` tasks blocked by unfinished dependencies.
- `afk tasks --status doing --json` shows active resource locks and claim
  timestamps.
- `afk take --dry-run --limit 0 --json --full` shows the exact ready set a
  worker can claim.

### How do I preview all ready tasks?

```sh
afk take --dry-run --limit 0 --json --full
```

Use a positive `--limit` for a bounded preview:

```sh
afk take --dry-run --limit 5 --json --full
```

Dry-run JSON previews truncate long task bodies unless you pass `--full`.

### How do I claim a task and also get queue counts?

```sh
afk take --lease 30m --worker codex:1 --summary
```

`--summary` includes the claimed task plus queue counts and
`ready_remaining` after the claim. With `--dry-run`, it returns the ready
preview plus queue counts without claiming work.

Use `--envelope` when you want the same top-level object style for both dry-run
and claimed output. Dry-runs return `claimed:false`, `tasks`, and `queue`;
claims return `claimed:true`, `task`, and `queue`.

### What worker id should I use?

Use something stable enough to identify the claimant in logs:

```sh
afk take --lease 30m --worker "$USER:$$"
afk take --lease 30m --worker "codex:docs"
```

Worker ids are most useful when diagnosing abandoned `doing` tasks.

## Dependencies and resource locks

### How do dependencies work?

Add the prerequisite first, then add dependent work with `--blocked-by`:

```sh
first=$(afk add "fix the API contract")
afk add --blocked-by "$first" "update docs after the API contract lands"
```

The dependent task remains `todo` until the prerequisite is `done`.

A dependency is a `blocks` relation. `--blocked-by` is the shorthand for it. You
can also create relations after the fact:

```sh
afk relate <task-id> <related-id> --type blocks|relates|duplicates|parent
```

The `--type` defaults to `blocks`. Only `blocks` gates readiness; `relates`,
`duplicates`, and `parent` are informational links that never block a task from
being claimed.

### How do I create an independent task when my script may pass a blocker?

Use `none`:

```sh
afk add --blocked-by none "independent follow-up"
```

This is useful for generated add commands that always include a blocked-by slot.

### How do resource locks work?

Tasks with the same non-empty resource key are serialized while one is `doing`.
This prevents two workers from editing the same repo or other shared resource at
the same time.

```sh
afk add --resource repo:/path/to/project/code/project "task one"
afk add --resource repo:/path/to/project/code/project "task two"
```

After one task is claimed, the other stays `todo` but not ready until the active
task is finalized.

### How do I disable the default repo resource?

Use `--resource none`:

```sh
afk add --resource none "independent task"
```

Use this only when the work truly does not conflict with other tasks in the
same repository or shared resource.

### How do gates work?

A gate is a named precondition on a task. While a task has any unsatisfied gate,
it stays out of the ready set.

```sh
afk gate add <id> <name>
afk gate satisfy <id> <name>
```

`afk gate add` is idempotent. `afk gate satisfy` is one-way; satisfying an
unknown gate name errors. Use gates to hold work on an external condition such
as a review being approved or CI turning green.

A typical flow:

```sh
id=$(afk add "ship the release")
afk gate add "$id" ci-green
afk take --dry-run --limit 0 --json --full   # does not surface the task
afk gate satisfy "$id" ci-green
afk take --dry-run --limit 0 --json --full   # now surfaces the task
```

### What should I do with a bad dependency or resource shape?

Prefer creating a corrected task and hiding the old one:

```sh
afk add --blocked-by "$right_id" "corrected dependent task"
afk set "$old_id" deleted --note "superseded by corrected dependency"
```

Use `failed` instead of `deleted` when the old task records a real attempted
failure that should remain visible in failure reports.

## Finishing, failing, deleting, and retrying

### How do I mark work done?

```sh
afk set "$id" done --note "implemented and tested" --summary
```

Use `--json` when another tool or agent needs a structured confirmation:

```sh
afk set "$id" done --note "implemented and tested" --json
```

Use `--note-file -` when the evidence contains quotes, `&&`, or multiple
lines:

```sh
printf '%s\n' "$evidence" | afk set "$id" done --note-file - --summary
```

### Why should finalization print a confirmation?

`afk set` prints a small confirmation on success, such as the task id and new
status. That gives humans and agents a visible checkpoint while remaining easy
for scripts to ignore. Use `--json` when the caller needs a parseable result,
or `--summary` when the caller also needs queue counts in the receipt.

### How do I record failure?

```sh
afk set "$id" failed --note "missing credentials" --summary
```

Good failure notes name the blocking condition and the smallest next action,
not just "failed".

### Should I delete or fail obsolete work?

Use `deleted` for obsolete, duplicate, or superseded work that should disappear
from default lists:

```sh
afk set "$id" deleted --note "superseded by task 42"
```

Use `failed` when a worker attempted the task and hit a real blocker or
execution failure:

```sh
afk set "$id" failed --note "test environment requires credentials" --summary
```

Deleted tasks remain inspectable through:

```sh
afk tasks --status deleted
afk task "$id"
```

### How do I retry a failed task?

For a targeted retry, reopen the same task with a reason:

```sh
afk task "$id" --json
afk retry "$id" --reason "fixed the blocker"
# do the work
afk set "$id" done --note "verified" --summary
```

The default manual `retry` moves the task to `doing`, clears stale task-level
error text, and opens a new attempt. Prior failed attempts remain in history.
To schedule the same failed task for later instead, use:

```sh
afk retry "$id" --disposition deferred --available-at 2026-07-18T13:00:00Z --reason "wait for maintenance window"
```

Deferred retry returns the task to `todo` and does not open an attempt until a
worker claims it after `available_at`.

### Why is `afk retry` a command if retry is just `doing`?

Retry is still modeled as a normal status transition. The command is narrow
sugar for the common failed-task recovery path:

```sh
afk retry "$id" --reason "fixed the blocker"
```

That is equivalent to:

```sh
afk set "$id" doing --note "retrying: fixed the blocker"
```

If you do not want to retry the exact task now, return it to the queue:

```sh
afk set "$id" todo --note "ready for another worker"
```

### What if I accidentally marked a failed task done?

Inspect the task:

```sh
afk task "$id" --json
```

Direct manual finalization is auditable: AFK records terminal attempts even when
there was no open attempt. If the work was not actually done, set it back to an
appropriate status with a clear note:

```sh
afk retry "$id" --reason "correcting accidental done"
```

or:

```sh
afk set "$id" failed --note "accidental done; work still blocked" --summary
```

## Abandoned or stale `doing` tasks

### How do I find work that is stuck in `doing`?

```sh
afk tasks --status doing --json
afk task "$id"
```

Check the worker id, lease, started time, events, and attempts before changing
state.

### How do I recover an abandoned claim?

If the original worker is gone and the work should not remain active, close the
attempt explicitly:

```sh
afk set "$id" failed --note "orphaned doing claim" --force --summary
```

Then either add a fresh task or retry the same one:

```sh
afk add "resume the abandoned work from task $id"
```

or:

```sh
afk retry "$id" --reason "orphaned work"
```

Use the retry form when the same task body is still the correct execution
contract.

## Snapshots and audit trails

### When should I use `afk snapshot`?

Use snapshots when a task or review asks for before/after queue evidence:

```sh
afk snapshot --label before --output before.json
afk snapshot --label after --task "$id" --output after.json
```

Snapshots are read-only JSON. They include counts, ready tasks, todo tasks,
doing tasks, and optional task details.

### Why not compare `afk status` output manually?

Manual comparisons are easy to lose once a task is claimed or completed.
Snapshots create durable evidence at the moment you need it.

### What is the difference between `afk task` and `afk snapshot --task`?

Use `afk task <id>` when you need one task's full record.

Use `afk snapshot --task <id>` when you need that task record plus queue context
in one JSON evidence artifact.

## Worker loops and automation

### How do I replace `afk run`?

Use the built-in `afk loop` worker-driver (`afk loop --command '...' --max-tasks N`; see runner.md), or own the execution loop outside AFK:

```sh
task_json=$(afk take --lease 30m --worker "$USER:$$" --summary)
test -n "$task_json" || exit 0
id=$(printf '%s\n' "$task_json" | jq -r .task.id)
body=$(printf '%s\n' "$task_json" | jq -r .task.body)

if agent-command "$body"; then
  afk set "$id" done --note "agent-command completed" --worker "$USER:$$" --summary
else
  afk set "$id" failed --note "agent-command failed" --worker "$USER:$$" --summary
fi
```

AFK owns durable queue state. Your wrapper owns process lifetime, logs, retries,
tool permissions, and agent selection.

### Why does the worker loop test for empty output?

`afk take` intentionally leaves stdout empty when there is no claim. That makes
shell loops simple and avoids treating "no ready task" as a task payload.

### Should automation use text output or JSON?

Use JSON for automation:

```sh
afk take --summary
afk take --dry-run --json --full --envelope
afk tasks --status todo --json
afk task "$id" --json
afk set "$id" done --note "verified" --json
```

Plain text is for humans and quick terminal checks.

## Discovery and queue hygiene

### What is `afk prompt --discover`?

It prints a read-only discovery workflow for an agent or human reviewer:

```sh
afk prompt --discover
```

It does not add tasks. A discovery pass should classify the target, gather
current evidence, check the queue for duplicates, validate candidate task bodies
with `afk add --dry-run`, then ask one enqueue confirmation.

### What makes a discovery candidate queueable?

It should be:

- current: proven by live files, command output, queue history, or docs/source mismatch
- atomic: one behavior, package, command, doc, dataset, or media set
- bounded: roughly under one hour
- verifiable: exact local check included
- rejectable: clear `Reject-if:` condition included

Avoid vague bodies like "improve docs", "continue cleanup", or "investigate the
repo". Convert them into specific mini-specs or reject them.

### How do I validate discovery output?

For each candidate:

```sh
afk add --dry-run --json --cwd /path/to/repo --source task-discovery --tag discovery --resource repo:/path/to/repo "<task body>"
```

Only enqueue candidates that pass dry-run validation and remain non-duplicates
after `afk find` and `afk tasks --status todo/doing` checks.

## Web UI and API

### How do I start the dashboard?

```sh
afk serve
```

Use a custom address when needed:

```sh
afk serve --addr 127.0.0.1:8080
```

The dashboard is a visibility and action layer over the same queue.

### Why did `afk serve` fail to start?

Check:

- The address is valid, for example `127.0.0.1:8080`.
- The port is not already in use.
- The configured queue path is writable.
- Your environment is allowed to open a browser, if browser opening is enabled.

Try a random local port:

```sh
afk serve --addr 127.0.0.1:0
```

## Output and scripting

### Why does one command print text and another JSON?

Commands default to human-friendly output unless JSON is the safer default for
worker payloads. For scripts, pass `--json` explicitly wherever the command
supports it.

### How do I get only counts?

```sh
afk status --summary
afk status --summary --json
```

Without `--summary`, `afk status` also includes todo and doing task lists.
Doing tasks include derived claim diagnostics. Text output appends claim age
and stale reason when available; JSON output adds a `claim` object:

```json
{
  "age_seconds": 1860,
  "stale": true,
  "reason": "lease_expired"
}
```

`stale` and `reason` are omitted when the claim is still fresh. Add `--blocked`
when you need to see which unfinished dependencies are blocking todo tasks.

### How do I list everything, including deleted tasks?

```sh
afk tasks --status all
afk tasks --status all --json
```

Use `--status deleted` when you only want hidden tasks.

## Data safety and direct SQLite access

### Can I edit the SQLite database by hand?

Avoid direct writes. Use AFK commands so events, attempts, dependencies, status
migration, and resource locks stay coherent.

Read-only SQLite inspection is fine when debugging, but prefer:

```sh
afk task "$id" --json
afk snapshot --label debug --output debug.json
```

### Does `deleted` remove task history?

No. `deleted` hides the task from default listings while preserving inspection
through `afk task <id>` and `afk tasks --status deleted`.

### What happens to old `pending` or `working` statuses?

Stored legacy statuses are migrated to current names:

- `pending` -> `todo`
- `working` -> `doing`

Use only `todo`, `doing`, `done`, `failed`, and `deleted` in new commands.

## Error handling

### `task not found`

Check the id and queue path:

```sh
afk find "$id" --json
afk tasks --status all --json
afk --queue /expected/path/tasks.sqlite task "$id"
```

### `dependency not found` or invalid dependency

Inspect the prerequisite id:

```sh
afk task "$blocked_by_id"
```

Then add with a real id or use `--blocked-by none` for an independent task.

### `dependency cycle`

The dependency would make a loop. Add a new corrected task chain instead of
trying to force the cycle:

```sh
first=$(afk add "true prerequisite")
afk add --blocked-by "$first" "dependent task"
```

Then mark the bad task `deleted` or `failed` with a note.

### `database is locked`

AFK retries transient SQLite busy errors internally. If the error persists:

- check for a long-running process using the same queue
- retry the command after the active writer finishes
- avoid direct SQLite writes while AFK commands are running
- confirm the queue is on a local filesystem rather than a flaky sync mount

### `invalid status`

Use one of:

```text
todo doing done failed deleted
```

Old command names like `done` or `fail` are not statuses by themselves. Use:

```sh
afk set "$id" done --note "verified"
afk set "$id" failed --note "reason"
```

## Practical recipes

### See what a new worker should do next

```sh
afk status --summary
afk take --dry-run --limit 5 --json --full
```

### Claim one task, work it, and finalize

```sh
task_json=$(afk take --lease 30m --worker "$USER:$$" --summary)
test -n "$task_json" || exit 0
id=$(printf '%s\n' "$task_json" | jq -r .task.id)
afk task "$id"
# do the work
afk set "$id" done --note "verified" --worker "$USER:$$" --summary
```

### Retry a specific failed task

```sh
afk task "$id" --json
afk retry "$id" --reason "fixed blocker"
# do the work
afk set "$id" done --note "verified on retry" --summary
```

### Preserve evidence around a queue operation

```sh
afk snapshot --label before --output before.json
afk set "$id" done --note "verified" --json
afk snapshot --label after --task "$id" --output after.json
```

### Hide duplicate work

```sh
afk task "$duplicate_id"
afk set "$duplicate_id" deleted --note "duplicate of task $canonical_id"
```

### Move one failed task back to the ready queue

```sh
afk set "$id" todo --note "ready for another worker"
```

Use this when no worker is actively retrying it now. Use `retry` when you are
starting the retry yourself.

## Where to read next

- [command-reference.md](command-reference.md) for exact command flags.
- [getting-started.md](getting-started.md) for the shortest happy path.
- [tasks.md](tasks.md) for metadata and task history.
- [scheduling.md](scheduling.md) for readiness, dependencies, and resource locks.
- [workers.md](workers.md) for claim/finalize flows.
- [runner.md](runner.md) for external worker-loop patterns.
- [configuration.md](configuration.md) for queue path behavior.
- [task-discovery.md](task-discovery.md) for discovery and enqueue validation.
