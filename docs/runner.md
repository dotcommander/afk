# driving the queue

There are two supported ways to drive work off the queue: the built-in
`afk loop` worker-driver, and an external loop you write around `afk take` /
`afk set`. Both are valid. `afk run` is no longer part of the public binary.

## afk loop (built-in worker-driver)

`afk loop` claims ready tasks and runs an agent command per task, with no
external supervisor. Each iteration:

1. Claims the first ready task (taking an exclusive lease).
2. Renders the configured `prompt_template` against the task.
3. Runs the configured `command` with that prompt, extending the lease on the
   `heartbeat` interval while it runs.
4. Finalizes the task `done` or `failed` from the command exit status.
5. Pauses for `cooldown` when no task is found, then repeats.

The agent command and prompt template come from `~/.config/afk/loop.yaml`
(written with defaults on first run — see configuration.md). `command` is empty
by default, so the loop is fail-closed:

```sh
afk loop
# Error: no agent command configured (set 'command' in
# ~/.config/afk/loop.yaml or pass --command)
```

Configure `command` in `loop.yaml`, or pass it for a single run. Use
`--max-tasks N` for a bounded run that exits cleanly after N tasks:

```sh
afk loop --command 'claude -p {{.Prompt}}' --max-tasks 5
```

Loop results are emitted as JSONL on stdout; agent output goes to stderr. Any
flag overrides the matching `loop.yaml` value for that run
(`--worker`, `--lease`, `--timeout`, `--cooldown`, `--heartbeat`,
`--max-failures`). The loop halts after `--max-failures` consecutive task
failures.

## external loop (you own the process)

Write your own loop when you want process supervision, custom retry policy, or
logging the built-in driver does not provide.

Preview readiness without claiming:

```sh
afk take --dry-run --limit 5 --json --full
```

A minimal external loop body:

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

This keeps AFK focused on durable task state, readiness, claims, prompts, and
visibility. The caller owns execution policy, retries, logs, and process
lifetime.
