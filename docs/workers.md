# workers

A worker is anything that claims a ready task, executes the task body, and records the outcome.

Preview ready work without mutation:

```sh
afk take --dry-run --limit 5 --json --full
```

Claim one task:

```sh
afk take --lease 30m --worker codex:1 --summary
```

The claimed task moves to `doing` and is printed as JSON. With `--summary`,
the receipt also includes queue counts and `ready_remaining`. Use the task
`id`, `body`, `cwd`, tags, resource key, and history as execution context.

Finalize explicitly:

```sh
afk set 1 done --note "verified" --summary
afk set 1 failed --note "missing credentials" --summary
```

Use `--note-file -` when evidence contains shell-awkward characters:

```sh
printf '%s\n' "$evidence" | afk set 1 done --note-file - --summary
```

Retry one specific failed task by opening a new attempt directly:

```sh
afk task 1 --json
afk retry 1 --reason "fixed the blocker"
# do the work
afk set 1 done --note "verified" --summary
```

`retry` moves the task to `doing`, clears stale task-level errors, and records a
fresh attempt. Terminal `done` and `failed` transitions close the open attempt;
if a manual terminal transition has no open attempt, AFK records a synthetic
terminal attempt so history stays auditable.

Recover abandoned claims by inspecting first, then closing the stale attempt:

```sh
afk tasks --status doing --json
afk task 1
afk set 1 failed --note "orphaned doing claim" --summary
afk add "resume the abandoned work from task 1"
```

There is no built-in `afk run` process supervisor. To automate execution, wrap the same primitives:

```sh
while task_json=$(afk take --lease 30m --worker "worker:$$" --summary); do
  test -n "$task_json" || break
  id=$(printf '%s\n' "$task_json" | jq -r .task.id)
  body=$(printf '%s\n' "$task_json" | jq -r .task.body)
  if agent-command "$body"; then
    afk set "$id" done --note "agent-command completed" --summary
  else
    afk set "$id" failed --note "agent-command failed" --summary
  fi
done
```
