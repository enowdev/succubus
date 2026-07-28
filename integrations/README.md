# Integration snippets

Ready-to-paste config for each tool, if you would rather not run
`succubus init`. Replace `/ABSOLUTE/PATH/TO/succubus` with the real path —
`command -v succubus` will tell you.

Use an **absolute path**. Agent tools frequently do not inherit your shell's
`PATH`, so a bare `succubus` often resolves to nothing.

| File | Tool | Merge into |
|---|---|---|
| `claude-code.json` | Claude Code | `~/.claude/settings.json` |
| `factory-droid.json` | Factory Droid | `~/.factory/hooks.json` |
| `codex.json` | Codex CLI | `~/.codex/hooks.json` |
| `codex-config.toml` | Codex CLI | `~/.codex/config.toml` |
| `gemini.json` | Gemini CLI | `.gemini/settings.json` |
| `cursor-mcp.json` | Cursor CLI | `.cursor/mcp.json` |
| `opencode.json` | OpenCode | `opencode.json` |
| `copilot-mcp.json` | Copilot CLI | `~/.copilot/mcp-config.json` |

These are **fragments to merge**, not whole files — dropping one on top of an
existing config will discard your other settings. `succubus init` merges for you
and keeps a `.succubus-bak` copy of anything it edits.

OpenCode additionally needs the plugin copied into the project, since it has no
shell-hook system. `succubus setup` does this for you; by hand it is:

```bash
mkdir -p .opencode/plugin
cp ../assets/opencode/succubus.ts .opencode/plugin/
```

See [../docs/SETUP.md](../docs/SETUP.md) for what each event does and how to
verify it is working.
