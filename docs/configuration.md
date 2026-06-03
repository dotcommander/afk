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

The queue path is the only setting controlled by flag/env. There is no config
file for it.

## config files

`afk loop` and `afk goal` read operator settings from YAML files under the user
config dir (`~/.config/afk/` on Linux/macOS). Each file is written with defaults
on first run; edit it in place. These are plain `yaml.v3` files — there is no
viper layer and no env-var overrides for these settings (per-run flags do
override the file).

### ~/.config/afk/loop.yaml

Settings for the `afk loop` worker-driver:

```yaml
command: ""                 # agent command template, e.g. "claude -p {{.Prompt}}"
                            # empty by default — fail-closed; loop errors until set
prompt_template: |          # text/template rendered against the task
  Task ID: {{.ID}}
  Status:  {{.Status}}

  {{.Body}}
task_timeout: 10m0s          # cap on a single task execution
cooldown: 5s                 # pause between ticks when no task is found
max_consecutive_failures: 3  # halt after this many back-to-back failures
lease: 30m0s                 # exclusive claim duration per task
heartbeat_interval: 2m0s     # how often the lease is extended while running
worker: ""                   # worker identity; empty derives "loop-<pid>"
```

### ~/.config/afk/goal.yaml

Settings for the `afk goal` workflow:

```yaml
setup_command: ""            # agent command template for contract compilation
                            # empty by default — fail-closed; goal errors until set
audit_command: ""            # agent command for the independent auditor
                            # empty by default — goal audit errors until set
setup_prompt_template: ...   # prompt that compiles the objective into a contract
audit_prompt_template: ...   # prompt the auditor uses to check real artifacts
max_tokens: 0                # per-goal token budget (0 = unlimited)
max_iterations: 0            # per-goal iteration cap (0 = unlimited)
max_duration: 0s             # per-goal wall-clock cap (0 = unlimited)
token_regex: ""              # regex to parse a token count from agent output
setup_timeout: 5m0s          # cap on the setup (compile) agent call
audit_timeout: 5m0s          # cap on the audit agent call
```

Both `setup_command` and `audit_command` are empty by default: the workflow is
fail-closed, so `afk goal` errors until you configure a setup command and
`afk goal audit` errors until an audit command is configured.

The token budget (`max_tokens`) only trips when the agent emits a count that
`token_regex` can parse; with the default empty `token_regex`, recorded token
usage stays `0` and the token cap never fires. The iteration
(`max_iterations`) and wall-clock (`max_duration`) caps always apply.
