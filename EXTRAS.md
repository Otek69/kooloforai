# Koolo AI Extras

`koolo-extras.exe` runs optional features **next to** Koolo, as a separate program:

- **Koolo can't crash because of an extra.** The extras only talk to Koolo through its MCP server.
- **Extras can't crash each other.** Each feature is a module. A module that crashes is restarted; after 3 crashes in 10 minutes it's switched off, and the dashboard and an alert tell you. Everything else keeps running.
- **Each module can be switched on or off** in `config\extras.yaml` and fixed on its own. Once a module has proven itself, it can be moved into Koolo itself.

Start: **Start menu → Koolo AI → Koolo AI Extras** (it asks for admin rights once, so it can restart Koolo).
Dashboard: **http://localhost:8088/** (Start menu: *Extras dashboard*).

## Modules

| Module | What it does |
|---|---|
| **stats** | Records every game (result, level, experience, Koolo version) and level and XP every 30 s. Dashboard: games and deaths today, XP per hour, deaths per game, level curve, and **comparison by Koolo version** (did a fix help?). Alerts on milestones (level 10, 20, …) and death streaks |
| **watchdog** | Restarts Koolo if it stops. On a **crash report, a burst of errors, or a character stuck** for 25 minutes, it stops the bot and starts **Claude Code** with `/fix-koolo` and the details, on a new git branch `autofix/<date>`. If the same problem returns within 30 minutes, it **rolls back** to the previous version and rebuilds. Limits: 3 fixes per day, 30 minutes between fixes, never pushes |
| **autopilot** | Every 10 games, runs the brain "check" automatically: reviews events and deaths, saves lessons, **gear advisor** (points out stashed upgrades), sets the plan and comments on the overlay. Uses Claude Code on your Claude plan. Off by default |
| **patchwatch** | Notices a D2R update, runs Koolo Doctor, and if Koolo can no longer read the game, alerts you and asks the watchdog for a fix |
| **alerts** | Sends alerts to **Discord** (webhook URL in `extras.yaml`) and shows them on the dashboard |
| **session** | Saves every play session as a folder in `extras\sessions\` for the **video pipeline**: log, screenshots, timing, prompt. With OBS enabled it also starts and stops recording. Then: `d2live-edit write --session <folder>` and `d2live-edit render --session <folder>` |
| **dashboard** | The page above, plus **http://localhost:8088/overlay**, a "Day N / level / deaths / games / XP per hour" banner for OBS (Browser source) |

## Setup

1. Install the update (it adds `koolo-extras.exe` and `config\extras.yaml`; your other settings stay).
2. For **auto-fix and autopilot**: install Go, Git and Claude Code, run `git clone https://github.com/Otek69/kooloforai`, then put that folder in `repoDir:` in `extras.yaml`. Run `claude` in it once to log in and approve the `koolo` MCP server. Without `repoDir`, problems are still detected and reported, just not fixed.
3. Optional:
   - Discord webhook in `alerts.discordWebhook`.
   - `challenge.startDate` for "Day N".
   - `autopilot.enabled: true`.
   - `session.obs.enabled: true`, plus the OBS WebSocket password.
4. Start Koolo AI, then **Koolo AI Extras**.

`autofix: settings` lets Claude change only settings, never code. `autofix: off` only alerts.

## Reviewing auto-fixes

Each fix is a git branch in your kooloforai folder, `autofix/<date-time>`, with a commit message explaining cause, change and verification. The dashboard lists every fix and its outcome:
- `holds`: the problem didn't come back.
- `rolled_back`: the problem came back, so the previous version was restored.

Look at a fix with `git log -p autofix/...`. To keep it, merge it into your main branch; otherwise delete the branch.

## When an extra itself breaks

The dashboard shows the module as `restarting` or `disabled`, with the last error, and the full log is in `extras\logs\`. Open Claude Code in the kooloforai folder and say, for example: *"the stats extra is disabled, here's the error: …"*. The code is in `internal/extras/`, one file per module, with tests (`go test ./internal/extras/`). `scripts/dev-rebuild.ps1` builds the new `koolo-extras`, which swaps itself in on its next start.
