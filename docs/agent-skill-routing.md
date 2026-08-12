# Agent Skill Routing

CodexLoom can disable an installed Codex Skill for one adopted Agent without
changing the shared Skill, the Agent Profile, or another Agent's Skill policy.
The V1 policy is intentionally narrow: it stores Agent-scoped disables by the
absolute path to `SKILL.md`. Enabling a path removes that Agent exception and
falls back to Codex's shared user policy.

## Storage

Agent exceptions are stored in:

```text
<data-dir>/agent-skill-config.json
```

The file is keyed by stable Agent ID. Each record contains sorted absolute
`SKILL.md` paths and an update timestamp. It is separate from Profiles because
Skill selection is runtime configuration, not durable identity or Domain
scope.

CodexLoom compiles the disabled paths into the Codex app-server `config`
parameter on `thread/start` and cold `thread/resume`:

```json
{
  "skills": {
    "config": [
      {
        "path": "/absolute/path/to/SKILL.md",
        "enabled": false
      }
    ]
  }
}
```

This is a Codex SessionFlags override. It is evaluated after the shared user
Skill policy and does not write `~/.codex/config.toml` or a project
`.codex/config.toml`.

## CLI

Inspect one Agent's projected inventory and application state:

```sh
loom skills agent AGENT
```

Disable an exact Skill path for that Agent:

```sh
loom skills agent AGENT disable /absolute/path/to/SKILL.md
```

Remove the exception:

```sh
loom skills agent AGENT enable /absolute/path/to/SKILL.md
```

The target must be an adopted, idle Agent. The path must be absolute and end in
`SKILL.md`.

## HTTP API

```text
GET   /api/agents/{agent}/skills
PATCH /api/agents/{agent}/skills/config
```

The PATCH body is:

```json
{
  "path": "/absolute/path/to/SKILL.md",
  "enabled": false
}
```

The response includes both the persisted `config` and projected `inventory`.
`disabledPaths` expresses desired policy. `applied` and `restartRequired`
separately report whether a loaded Codex Thread has that exact policy. When no
Thread runtime is loaded, both are false and the desired policy applies on the
next cold load.

The existing CodexHost `skills/list` `data` array remains unchanged for
compatibility. CodexLoom adds an `agents` projection because multiple Agents
may share one CWD while applying different SessionFlags policies.

## Runtime Boundary

Codex app-server applies the generic `config` override when a Thread is first
started or cold-resumed. It does not replace config for a Thread that is already
loaded and subscribed in the same app-server process. Therefore:

1. Updating an idle Agent persists the desired policy immediately.
2. If that Agent's Thread is already loaded, the API reports
   `restartRequired=true` and `applied=false`.
3. CodexLoom refuses to start another Turn on that stale loaded Thread.
4. A managed CodexLoom restart lets the next Turn cold-resume the Thread with
   the persisted policy.
5. After that cold load, inventory must show the target path disabled with
   `applied=true` and `restartRequired=false`.

Do not claim a hot `thread/resume` changed the policy. Do not modify the shared
Skill file to work around this boundary.

## Compatibility

- Agents without a stored exception keep the existing shared Codex behavior.
- Removing the last exception deletes that Agent's record and passes an empty
  SessionFlags Skill list on the next cold load.
- Missing or undiscovered paths remain visible as configured exceptions so an
  operator can remove them explicitly.
- Edge-discovered Agents must be adopted before CodexLoom stores runtime Skill
  policy for them.
