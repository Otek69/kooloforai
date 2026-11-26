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
