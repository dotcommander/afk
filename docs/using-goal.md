# Using `afk goal`

`afk goal` compiles a free-text objective into an **approved, dependency-ordered task chain** in your queue. You describe the outcome; a configured *setup agent* turns it into a structured contract; you approve it; afk inserts the contract's tasks so a worker — or [`afk loop`](using-loop.md) — can drain them. An optional *audit agent* independently verifies completion.

```
objective ──▶ setup agent ──▶ contract ──▶ you approve ──▶ task chain ──▶ (worker drains) ──▶ audit
   text         (LLM)          (JSON)        [yes/no]        in queue
```

## 1. Configure an agent (required)

`afk goal` is **fail-closed**: it errors until you tell it how to call your agent. Configuration lives at `~/.config/afk/goal.yaml`, written with defaults on first run:

```yaml
setup_command: pi -p {{.Prompt}}   # compiles the objective into a contract
audit_command: pi -p {{.Prompt}}   # the independent completion auditor
# ... prompt templates, budget caps, timeouts ...
```

- `{{.Prompt}}` is replaced with the rendered prompt — it becomes a single argument even with spaces or newlines. Put your agent CLI here (`pi -p {{.Prompt}}`, `claude -p {{.Prompt}}`, etc.).
- Override per run with `--setup-command` / `--audit-command` (handy for testing with a stub script).
- Until `setup_command` is set, `afk goal` errors; `goal audit` likewise needs `audit_command`.

## 2. Preview the contract (`--dry-run`)

`--dry-run` runs the setup agent and prints the compiled contract **without queueing anything**. Add `--json` for machine-readable output:

```sh
afk goal --dry-run --json "add CSV export to the report command"
```

```json
{"outcome":"report command supports CSV export","done_criteria":["`afk report --csv` emits valid CSV","existing JSON output unchanged"],"must_do":["add a --csv flag to the report command"],"avoid":["breaking the default JSON output"],"philosophy":"smallest change that satisfies the done criteria","tasks":["add --csv flag to the report command","implement the CSV encoder","add a unit test asserting valid CSV output"]}
```

The contract's `tasks` array is the ordered work; each later task is queued blocked by the previous one.

## 3. Approve and queue

Without `--dry-run`/`--json`, afk prints the contract, then prompts on **stderr**:

```
Approve contract? [yes/no]:
```

Only an explicit `yes` queues the tasks. On approval afk prints a **receipt** to **stdout**:

```sh
echo yes | afk goal "add CSV export to the report command"
```

```json
{"goal_id":"e74d05ef-0819-4dbe-990b-568936b6c369","tasks":3}
```

> **Capturing the goal_id in scripts.** The contract and receipt go to **stdout**; the `Approve contract?` prompt goes to **stderr**. Redirect stderr away and read the last stdout line:
>
> ```sh
> goal_id=$(echo yes | afk goal "…" 2>/dev/null | tail -1 | jq -r .goal_id)
> ```

## 4. Inspect the chain

`afk goal status <goalID>` shows the durable goal record and a live count of its tasks:

```sh
afk goal status e74d05ef-0819-4dbe-990b-568936b6c369
```

```json
{"id":"e74d05ef-0819-4dbe-990b-568936b6c369","objective":"add CSV export to the report command","outcome":"report command supports CSV export","status":"active","created_at":"2026-06-03T20:31:38Z","group_id":"e74d05ef-0819-4dbe-990b-568936b6c369","task_counts":{"todo":3}}
```

- `objective` — your **raw** words, exactly as typed.
- `outcome` — the setup agent's one-line restatement (kept for reference).
- `task_counts` — member tasks bucketed by status.

The tasks are normal queue entries grouped by `group_id` and tagged `source: goal:<goalID>`. Only the first is ready; the rest stay blocked until it finishes:

```sh
afk take --dry-run --json --full
```

```json
{"id":"1f1c995d-462c-41fc-a9ba-5ac90e6d5589","created":"2026-06-03T20:31:38Z","status":"todo","body":"add --csv flag to the report command","cwd":"/Users/vampire/go/src/afk","source":"goal:e74d05ef-0819-4dbe-990b-568936b6c369","group_id":"e74d05ef-0819-4dbe-990b-568936b6c369"}
```

Drain the chain with any worker — `afk take` / `afk set` by hand, or let [`afk loop`](using-loop.md) run it end-to-end.

## 5. Audit completion (optional)

After a task is finished, `afk goal audit <taskID>` runs the **independent** auditor — a fresh agent invocation that inspects the real artifacts and **does not trust the completion note**:

```sh
afk goal audit 1f1c995d-462c-41fc-a9ba-5ac90e6d5589
```

```json
{"approved":true,"disapproved":false,"output":"…"}
```

- The auditor judges against your **raw objective**, so it can catch a setup agent that misread the request.
- `<disapproved/>` — or any audit error/timeout, since disapproval is the fail-safe default — **re-queues the task to `todo`** so the work is retried.

## Budget & safety caps

`afk goal` enforces per-goal limits (also settable in `goal.yaml`):

| Flag | Cap |
|------|-----|
| `--max-iterations N` | stop after N agent iterations (0 = unlimited) |
| `--max-duration D` | wall-clock cap, e.g. `30m` (0 = unlimited) |
| `--max-tokens N` | token budget — only enforced when `token_regex` can parse a count from agent output (0 = unlimited) |

The objective is HTML-escaped before it reaches the prompt (it is untrusted data) and is capped at 4000 characters.

## Troubleshooting

| Symptom | Cause / fix |
|---------|-------------|
| `no setup command configured` | Set `setup_command` in `goal.yaml` or pass `--setup-command`. |
| `goal audit` errors immediately | `audit_command` is empty — configure it or pass `--audit-command`. |
| Approval seems ignored | Only the exact word `yes` approves; anything else declines and writes nothing. |
| Can't find the goal_id | It's the receipt line on **stdout**; the prompt is on stderr. Use `2>/dev/null \| tail -1`. |

## Flag reference

`afk goal <objective>`:

| Flag | Meaning |
|------|---------|
| `--setup-command T` | agent command template for contract compilation |
| `--audit-command T` | agent command template for the auditor |
| `--dry-run` | print the contract and exit without queueing |
| `--json` | print the contract as JSON and skip approval (does not queue) |
| `--cwd PATH` | working directory recorded on queued tasks |
| `--max-tokens N` / `--max-iterations N` / `--max-duration D` | per-goal caps |
| `--queue PATH` | queue DB path (or `AFK_QUEUE`) |

See also: [`afk loop`](using-loop.md) · [command reference](command-reference.md#goal) · [configuration](configuration.md).
