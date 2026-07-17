# Using `afk loop`

`afk loop` is afk's built-in **worker-driver**. It repeatedly claims the next ready task, runs your agent command against it, and records the result — draining the queue unattended. It is the simplest way to run a [goal](using-goal.md) chain, or any backlog, to completion.

```
┌── claim next ready task (lease it)
│   render the prompt from the task
│   run:  command   (e.g. pi -p {{.Prompt}})
│   exit 0 ▸ done    nonzero ▸ failed    timeout ▸ timeout
└── cooldown, then repeat
```

## 1. Configure an agent (required)

Like `goal`, `loop` is **fail-closed** — it needs a `command`. Config lives at `~/.config/afk/loop.yaml`:

```yaml
command: pi -p {{.Prompt}}     # the agent run per task (required)

prompt_template: |             # what the agent receives, rendered per task
  Task ID: {{.ID}}
  Status:  {{.Status}}

  {{.Body}}

task_timeout: 10m              # per-task execution timeout
cooldown: 5s                   # pause between ticks when no task was found
max_consecutive_failures: 3    # halt after this many failures in a row
lease: 30m                     # exclusive claim taken on each task
heartbeat_interval: 2m         # how often the lease is extended while running
worker: ""                     # identity (default: loop-<pid>)
```

- `command` is rendered like goal's: `{{.Prompt}}` becomes the per-task prompt (built from `prompt_template`) as a single argument.
- `prompt_template` sees the task fields: `{{.ID}}`, `{{.Status}}`, `{{.Body}}`, `{{.Tags}}`, `{{.Stage}}`.
- Every config value has a flag override (see the table below). Until `command` is set, `loop` errors.

## 2. Run it

```sh
afk loop                       # drain continuously using loop.yaml
afk loop --max-tasks 5         # process 5 tasks, then exit cleanly
afk loop --command 'pi -p {{.Prompt}}' --timeout 15m
```

Each tick:

1. **Claim** the next ready task and take an exclusive **lease**, so two workers never grab the same task.
2. **Render** the prompt from `prompt_template` and run `command`.
3. **Record** the outcome by the agent's exit status: exit 0 marks the task `done`; a timeout marks it `failed` and emits result status `"timeout"`; any other non-zero exit marks it `failed`. Terminal writes are fenced by the claiming worker. The lease is extended every `heartbeat_interval` while the agent runs; definitive ownership loss or lease expiry cancels the child and prevents stale finalization.
4. **Cooldown** after each task, then loop again.

After `max_consecutive_failures` failures in a row, the loop halts so a broken agent doesn't burn the whole backlog.

## 3. Stopping

- `--max-tasks N` — exit cleanly after N tasks (good for cron or CI bursts).
- `Ctrl-C` — the in-flight lease simply expires; an unfinished task returns to the ready pool.
- `--max-failures N` — automatic halt on repeated failures.

## Flag reference

| Flag | `loop.yaml` key | Meaning |
|------|-----------------|---------|
| `--command T` | `command` | agent command template (required) |
| `--timeout D` | `task_timeout` | per-task execution timeout |
| `--cooldown D` | `cooldown` | pause between ticks when idle |
| `--lease D` | `lease` | exclusive claim duration per task |
| `--heartbeat D` | `heartbeat_interval` | lease-extension interval while running |
| `--max-failures N` | `max_consecutive_failures` | halt after N consecutive failures |
| `--max-tasks N` | — | exit after N tasks (0 = unlimited) |
| `--worker S` | `worker` | worker identity (default `loop-<pid>`) |
| `--queue PATH` | — | queue DB path (or `AFK_QUEUE`) |

## `afk loop` vs the alternatives

| You want… | Use |
|-----------|-----|
| Unattended draining with one agent command | **`afk loop`** |
| An agent that re-plans each cycle inside Claude Code | Claude Code `/loop` calling `afk prompt` |
| Full manual control / custom supervision | external `afk take` → run → `afk set <id> done\|failed` |

`afk loop` and a `goal` chain compose directly: queue a goal, then run `afk loop` to drain its tasks in dependency order.

See also: [`afk goal`](using-goal.md) · [command reference](command-reference.md) · [configuration](configuration.md).
