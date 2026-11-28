# Koolo AI downloads

## Stable: v0.6.3-ai.5

| File | What it is |
|---|---|
| [KooloAI-v0.6.3-ai.5-update.zip](https://github.com/Otek69/kooloforai/raw/downloads/KooloAI-v0.6.3-ai.5-update.zip) | The programs only. Unzip into `%LOCALAPPDATA%\Programs\KooloAI` and overwrite |
| [KooloAI-Setup-v0.6.3-ai.5.exe](https://github.com/Otek69/kooloforai/raw/downloads/KooloAI-Setup-v0.6.3-ai.5.exe) | Installer, keeps your settings |

## Preview for testing: v0.6.3-ai.6-extras.8 (from the `extras` branch)

Flexible memory reader (address table you can extend in `config\memory-patterns.yaml`, addresses remembered per D2R build), self-healing character name and skill hotkeys, multi-pass memory reading for newer D2R builds, Koolo Doctor `--scan` (deep scan for new memory patterns). Adds **Koolo AI Extras** (`koolo-extras.exe`), a separate program next to Koolo:
- statistics dashboard and OBS banner
- auto-fix watchdog with rollback
- automatic brain checks
- D2R patch detection
- Discord alerts
- session export for videos

If an extra fails, Koolo keeps running. See EXTRAS.md.

| File | What it is |
|---|---|
| [KooloAI-v0.6.3-ai.6-extras.8-update.zip](https://github.com/Otek69/kooloforai/raw/downloads/KooloAI-v0.6.3-ai.6-extras.8-update.zip) | Programs plus `config\extras.yaml.dist`. Unzip into your Koolo AI folder; rename `extras.yaml.dist` to `extras.yaml` |
| [KooloAI-Setup-v0.6.3-ai.6-extras.8.exe](https://github.com/Otek69/kooloforai/raw/downloads/KooloAI-Setup-v0.6.3-ai.6-extras.8.exe) | Installer, keeps your settings and adds `extras.yaml` |

Source: `main` (stable), `extras` (preview). Use with offline characters only.
