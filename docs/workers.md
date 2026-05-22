# workers

A worker is anything that claims a ready task, executes the task body, and records the outcome.

Preview ready work without mutation:

```sh
afk take --dry-run --limit 5 --json
```

Claim one task:

```sh
afk take --lease 30m --worker codex:1
```

The claimed task moves to `doing` and is printed as JSON. Use its `id`, `body`, `cwd`, tags, resource key, and history as execution context.

Finalize explicitly:

```sh
afk set 1 done
afk set 1 failed "missing credentials"
```

Recover abandoned claims by inspecting first, then closing the stale attempt:

```sh
afk tasks --status doing --json
afk task 1
afk set 1 failed "orphaned doing claim"
afk add "resume the abandoned work from task 1"
```

There is no built-in `afk run` process supervisor. To automate execution, wrap the same primitives:

```sh
while task_json=$(afk take --lease 30m --worker "worker:$$"); do
  test -n "$task_json" || break
  id=$(printf '%s\n' "$task_json" | jq -r .id)
  body=$(printf '%s\n' "$task_json" | jq -r .body)
  if agent-command "$body"; then
    afk set "$id" done
  else
    afk set "$id" failed "agent-command failed"
  fi
done
```
