# configuration

`afk` stores tasks in SQLite.

Default path:

```text
~/.claude/queue/tasks.sqlite
```

Override per command:

```sh
afk --queue /tmp/tasks.sqlite status
```

Override through the environment:

```sh
AFK_QUEUE=/tmp/tasks.sqlite afk tasks
```

Non-`.sqlite` paths are normalized to a sibling `.sqlite` database. For example, `/tmp/tasks.jsonl` becomes `/tmp/tasks.sqlite`.
