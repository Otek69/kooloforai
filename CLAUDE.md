# Koolo AI: instructions for Claude Code

This repository is **Koolo AI**: the Koolo Diablo II: Resurrected bot (Go) with an AI brain. It is used with
**offline characters only**. Never add features for Battle.net/online play, multi-account farming, or detection evasion.

## Seeing the running bot

While Koolo runs, it serves an MCP server at `http://localhost:8087/mcp` (registered in `.mcp.json` as `koolo`).

| Tool | Use |
|---|---|
| `koolo_doctor` | Full health check: settings, characters, pickit, AI brain, D2R window, step-by-step memory test |
| `koolo_logs` | Tail of the newest log (`errorsOnly`, or a `character`'s supervisor log) |
| `koolo_crash_reports` | Newest crash report with stack trace |
| `koolo_status` | Per character: status, games, deaths, chickens, errors, drops, plus the AI brain's memory |
| `koolo_game_state` / `koolo_screenshot` | What the character is doing right now |
| `koolo_control` | start / stop / pause a character |
| `koolo_apply_change` | Validated settings change: pickit rule, run list, curse chicken, AI goal |
| `koolo_reload_config` | Reload config files |
| `koolo_events`, `koolo_brain_plan`, `koolo_add_lesson`, `koolo_say`, `koolo_last_failure` | Brain tools (used by Claude Desktop in `provider: desktop` mode, see CLAUDE-DESKTOP.md) |

If the tools aren't available, Koolo isn't running. Ask the user to start it.

## Fix loop

1. **Diagnose:** run `koolo_doctor`, then `koolo_logs` (errorsOnly), then `koolo_crash_reports`. Check `koolo_status`, and look with `koolo_screenshot` if the problem is visible.
2. **Settings or code?** Prefer settings changes (`koolo_apply_change`, or edit `config/` and run `koolo_reload_config`) when they fix it.
3. **Code fix:** find the cause in the source, make the smallest change that fixes it, and add or extend a test where the logic is testable (`go test ./internal/strategy/...`).
4. **Rebuild and restart:** `powershell -ExecutionPolicy Bypass -File scripts/dev-rebuild.ps1` builds, replaces the installed exes and restarts Koolo.
5. **Verify:** start the character with `koolo_control`, then watch `koolo_logs` and `koolo_status` until the problem is gone. Never claim it's fixed without this.
6. **Commit** on a branch with a clear message. Don't push unless the user asks.

## Code map

- `cmd/koolo`: main program. `cmd/koolo-doctor`: diagnostics tool.
- `internal/bot`: supervisors, game loop. `internal/run`: farming runs and leveling (`leveling_act*.go`).
- `internal/action`, `internal/character`: movement, combat, town, items, per-class logic.
- `internal/health`: potions, chicken, curse chicken. `internal/pather`: pathfinding.
- `internal/brain`: AI brain (planning, survival tuning, death lessons, assistant, local and Claude providers).
- `internal/strategy`: pure, testable game logic (experience filter).
- `internal/server`: web UI, assistant page, MCP server (`mcp.go`).
- `third_party/d2go`: the memory reader, patched (see the `kooloforai patch` comments). **After a D2R patch,** the address patterns in `pkg/memory/offset.go` and `diagnose.go` may need updating. `koolo_doctor` shows which ones are missing.

## Build

Windows binaries: `GOOS=windows go build ./cmd/koolo ./cmd/koolo-doctor`.
Release with installer: `scripts/build-release.sh <version>` (needs NSIS).
