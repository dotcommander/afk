# runner replacement

`afk run` is no longer part of the public binary.

Use `afk take --dry-run` for the readiness preview that `run --dry-run` used to provide:

```sh
afk take --dry-run --limit 5 --json --full
```

Use an external loop when you want process supervision:

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

This keeps AFK focused on durable task state, readiness, claims, prompts, and visibility. The caller owns execution policy, retries, logs, and process lifetime.
