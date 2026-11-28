# Debugging Koolo AI

When something goes wrong, there are three tools. All of them are in the Start menu under **Koolo AI**.

## 1. Koolo Doctor: run this first

**Koolo Doctor** checks your setup and prints `[OK]`, `[WARN]` or `[FAIL]` for each item. It asks for administrator rights because it reads the game's memory, like Koolo does.

| Check | What it tells you |
|---|---|
| System | Koolo and d2go (memory reader) versions, admin rights |
| Koolo settings | `koolo.yaml` readable, the D2R and D2 LoD 1.13c folders exist |
| Characters | Each character's config loads, and its pickit rules parse without errors |
| AI brain | `brain.yaml` is valid. For Claude it checks the **API key works**, without printing it. For a local model it checks the **model server is running** and the model is downloaded |
| Game | The D2R window size, and the **memory test**: enter a game with an offline character, and the doctor reads your name, level, area, life, monsters and items. If those look wrong, the memory reader doesn't match your D2R patch yet |
| Logs | The last error lines from the newest Koolo log, plus any crash reports |

A report is saved to `diagnostics\report-<time>.txt`.

## 2. Debug bundle: to get help

**Koolo Doctor - make debug bundle** runs the same checks and creates `diagnostics\koolo-debug-<time>.zip`, containing:
- the doctor report
- the last 5 logs
- crash reports
- the latest error screenshots
- your Koolo, character and brain configs
- the AI's memory and thoughts

**API keys, tokens, passwords, webhooks, usernames and emails are replaced with `<removed>`.** Still, give it a quick look before sharing it publicly.

## 3. Debug mode: watch it live

**Koolo AI (debug mode)** starts a console build of Koolo with `--debug`:
- every log line appears live in a console window, at the most detailed level
- error screenshots are saved to `screenshots\`
- crashes dump the state of everything that was running

The same switch works on the normal build: `koolo.exe --debug`, or set the environment variable `KOOLO_DEBUG=1`.

## Crash reports

If Koolo crashes, it now writes `crash-reports\crash-<time>.txt`, containing:
- the error and the full stack trace
- the Koolo, d2go and Go versions

The crash dialog shows where the file is. Koolo Doctor flags crash reports and includes them in the bundle.

## Common problems

| Symptom | Likely cause and fix |
|---|---|
| Doctor: "game data looks wrong" | D2R was patched after the bundled memory reader. Update d2go (`go get github.com/hectorgimenez/d2go@<newer>`) and rebuild |
| Doctor: "can't open the D2R process" | Not running as administrator |
| Doctor: Window not 1280x720 | Set D2R to windowed 1280x720 |
| Doctor: Claude API key rejected | Wrong or expired key, or no credit. Check platform.claude.com |
| Doctor: no model server | Start Ollama or LM Studio, or switch `provider` back to `anthropic` |
| Pickit rules error | A typo in a `.nip` file. The doctor prints which file and line |

## 4. Let Claude Code fix it (MCP)

While Koolo runs, it serves an **MCP server** at `http://localhost:8087/mcp`. It accepts connections only from your own PC, not from your network. Through it, Claude Code can run Koolo Doctor and read logs, crash reports, status, live game state and screenshots. It can also start, stop and pause characters and apply validated settings changes.

**Setup (once):**
1. Install [Go](https://go.dev/dl/) and [Git](https://git-scm.com/), and install Claude Code. It's included in Claude Pro.
2. Get the source: `git clone https://github.com/Otek69/kooloforai`
3. Start Koolo AI. Then, in the `kooloforai` folder, run `claude`. Claude Code finds the `koolo` MCP server from `.mcp.json` and asks you to approve it once.
4. Optional: run that terminal as administrator, so restarting Koolo doesn't show a UAC prompt every time.

**Use:**
- `/fix-koolo the bot keeps dying in the Pit`: Claude diagnoses with the tools, fixes the settings or code, rebuilds and restarts Koolo (`scripts/dev-rebuild.ps1`), then checks the fix worked in the logs and stats.
- `/improve-koolo leveling speed`: Claude looks at deaths, chickens, errors and the AI's run record, makes one change, and compares the numbers before and after.

Code changes are committed on a branch, so you can review them and undo them with git. Nothing is pushed unless you ask.

## Memory reader: addresses after a D2R patch

Koolo finds the game's data through address patterns. They are no longer hard-coded:

- **Built-in table**: `third_party/d2go/pkg/memory/patterns.yaml` (several variants per address possible).
- **Your own fixes**: `config/memory-patterns.yaml` (template: `memory-patterns.yaml.dist`). Tried first, no rebuild needed.
- **Cache per D2R build**: `config/memory-cache.json`. Every address found once is remembered for that exact
  D2R build (module size + build timestamp), so it keeps working even when D2R locks the code holding the
  pattern on a later start. The self-healing results (character name, skill hotkey table) are stored there too.
  A new D2R patch gets a new entry automatically.

`koolo-doctor.exe --scan` shows each address and where it came from (`pattern 1`, `cache`, `missing`).
`koolo-doctor.exe --reset-cache` clears the cache for the running build if an address turned out wrong.
Only missing **essential** addresses (GameData, UnitTable, UI, Hover, PanelManager) stop Koolo; the others
only switch off single features.
