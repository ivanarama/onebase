# OneBase MCP

`onebase mcp` exposes OneBase developer tooling to MCP-compatible AI clients.
It is a thin stdio wrapper over the CLI commands used in CI and local
development.

For project setup, client-specific verification status, and a checked Codex CLI
recipe, see [AI-assisted configuration development](vibe-coding.md).

Default mode is read-only. Mutating tools are hidden unless the server is
started with a specific write flag. `--allow-write` is still available as a
coarse switch for trusted local workflows, but the narrower flags are preferred.

## Start

```bash
onebase mcp --project /absolute/path/to/onebase-config
```

For a database-backed runtime:

```bash
onebase mcp --project /absolute/path/to/onebase-config --sqlite /absolute/path/to/database.db
```

Enable one write capability at a time for trusted local workflows:

```bash
onebase mcp --project /absolute/path/to/onebase-config --allow-refactor-write
```

Available write flags:

- `--allow-fmt-write` exposes `fmt_write`.
- `--allow-refactor-write` exposes `refactor_write`.
- `--allow-config-rollback` exposes `config_rollback`.
- `--allow-procrun` exposes `procrun`.
- `--allow-write` exposes all mutating tools.

Use the broad flag only when the client session is fully trusted:

```bash
onebase mcp --project /absolute/path/to/onebase-config --allow-write
```

## Client Config

Claude Desktop-style stdio config:

```json
{
  "mcpServers": {
    "onebase": {
      "command": "onebase",
      "args": [
        "mcp",
        "--project",
        "/absolute/path/to/onebase-config"
      ]
    }
  }
}
```

Write-enabled local config with refactor writes only:

```json
{
  "mcpServers": {
    "onebase": {
      "command": "onebase",
      "args": [
        "mcp",
        "--project",
        "/absolute/path/to/onebase-config",
        "--allow-refactor-write"
      ]
    }
  }
}
```

## Tools

Read-only:

- `check`
- `describe`
- `schema`
- `query`
- `eval`
- `examples`
- `impact`
- `refactor_preview`
- `config_versions`
- `config_diff`
- `widget_explain`
- `report_explain`
- `fmt_check`

Write-enabled with a specific flag, or all together with `--allow-write`:

- `fmt_write` via `--allow-fmt-write`
- `refactor_write` via `--allow-refactor-write`
- `config_rollback` via `--allow-config-rollback`
- `procrun` via `--allow-procrun`

## Recommended Agent Workflow

Ask the agent to work in this order:

1. Read `onebase://ai-guide` and `onebase://describe/compact`.
2. Use `schema` and `examples` before creating YAML.
3. Use `impact` or `refactor_preview` before rename/delete changes.
4. Use `check` after every proposed patch.
5. For database-backed configs, call `config_versions` before write tools and
   keep the latest snapshot ID for rollback.
6. Use the narrowest write flag needed for the task, after a clean preview and
   explicit human approval.

For larger changes, prefer CLI or configurator AI generation with partial apply:
it records tool trace, runs `check`, and creates config snapshots for
database-backed configurations.

## Safety Notes

- Keep read-only mode for exploration and review.
- Use absolute paths in client configuration.
- Treat generated changes as patches: inspect `refactor_preview`, `impact`, and
  `check` before enabling any write flag.
- In database-backed configurations, rollback is handled by `_config_versions`.
