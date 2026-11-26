# Koolo + Claude brain

This is Koolo v0.6.3 with two changes:

1. It uses the newest d2go memory reader I could find (October 2025).
2. It has an AI brain powered by Claude, in `internal/brain/`.

Koolo still does all of the real-time playing (moving, fighting, potions, looting). Claude makes the decisions and talks to your viewers.

## What the AI does

| When | What Claude does | Model |
|---|---|---|
| Before every game | Picks which of **your enabled runs** to do, and in what order, based on level, gear, resistances and its own run history | Opus |
| Before every game | Keeps a **long-term goal**, for example "reach level 40, then farm Mephisto for resistance gear" | Opus |
| Before every game | **Tunes survival**: potion and chicken thresholds, always kept within safe ranges | Opus |
| Death or chicken | Looks at the **screenshot of the moment it happened**, explains what went wrong, and writes down a **lesson** | Opus |
| Run finished, item stashed | Short on-stream reaction, including whether the item could be an upgrade | Haiku |

It has **memory** in `brain/memory.json` that is kept between sessions: success, death and chicken counts per run, items found per run, lessons learned, and the current goal. Over time it stops repeating runs that keep killing it and comes back to them once it is stronger. Delete the file to give it a fresh start.

**Safety limits:**
- The AI can only pick runs you enabled in Koolo's settings.
- Survival values are clamped. For example, the chicken threshold always stays between 15% and 50%.
- It stops calling the API at `maxSessionUSD`, and Koolo keeps playing your normal run list.
- If the API fails or there's no key, Koolo carries on exactly as before.

## Speedrun knowledge and smarter leveling

- **Experience-efficient leveling:** when Koolo levels a character through the Blood Moor, Cold Plains and Stony Field, it now skips lone weak monsters. It still kills boss groups, their minions, big packs and anything attacking it. This is the speedrunner rule that experience comes from boss groups, not trash. The Den of Evil still clears everything, because the quest needs it. The logic is in `internal/strategy/xpfilter.go`, with tests.
- **Route playbook:** the AI's planning, its death analysis and the assistant all get the route knowledge from MrLlamaSC's 18-part speedrun tutorial and the speedrun.com/d2lod leaderboard: level targets per zone, gear goals, boss tricks and how maps are laid out. Switch it off with `usePlaybook: false`.

## Run the brain on a local model (free)

In `config/brain.yaml`, set:

```yaml
provider: local
localURL: http://localhost:11434/v1   # Ollama; LM Studio is http://localhost:1234/v1
fastModel: qwen2.5vl:7b
smartModel: qwen2.5vl:7b
```

This needs no API key and costs nothing. Local models make weaker decisions than Claude, but Koolo still does all the real playing, so a weaker brain only affects the planning and commentary.

## AI Assistant panel

In Koolo's dashboard, click **AI** on a character card. The panel has:

- **Chat with Claude about your character.** It sees your live gear, inventory, stash, settings, run record and lessons. Quick buttons: *Runeword planner*, *Pickit rules for my build*, *Why do I die?*, *Best runs for me*, *Next upgrade*.
- **Suggested changes as cards.** Each card has an **Apply** button for a pickit rule, run list, curse-chicken list, or the AI's goal. Nothing changes until you click it. Pickit rules are checked by Koolo's rule parser first and saved to `ai_rules.nip` in your pickit folder.
- **The AI's memory:** its goal, run record, lessons learned and latest thoughts.
- **Armory:** everything equipped, on the mercenary, in the inventory, cube, stash and shared stash. The bot has to be running for this.

## Chicken on curses and auras

New character setting `health.chickenOnCurses` in `config/<character>/config.yaml`. Koolo leaves the game when one of these is on you:
`amplify_damage, decrepify, lower_resist, iron_maiden, conviction, fanaticism, might, weaken, terror, attract, dim_vision`.
For example, `chickenOnCurses: [amplify_damage, lower_resist]`. The AI assistant can suggest a list for you.

## Setup (Windows)

1. **Offline character only.** Never take this onto Battle.net.
2. Get an API key at **platform.claude.com**. Claude Pro doesn't include API access; the API is billed separately. Add a few dollars of credit and set a spending limit there too.
3. Set the key as an environment variable. Open PowerShell and run:
   ```
   setx ANTHROPIC_API_KEY "sk-ant-..."
   ```
   Then log out and back in. Or paste the key into `config/brain.yaml` as `apiKey`, but then never share that file.
4. Set up Koolo as usual (see `README.md`): game path, character, runs, pickit. Enable **several runs**, because the AI's choices come from that list.
5. Edit `config/brain.yaml` if you like: persona, budget, which models to use.
6. Start `koolo.exe`. The log should show `brain: enabled`.

## OBS

- **AI thoughts:** add a *Text (GDI+)* source, tick **Read from file**, and pick `brain/overlay.txt` next to `koolo.exe`. It shows the last few thoughts and the running API cost.
- **For editing:** `brain/thoughts.jsonl` has every thought with a timestamp.

## Building from source

Install Go 1.25 or newer, then run `build.bat` or `better_build.bat`.

## If it doesn't work

A D2R patch after October 2025 may have moved memory locations that d2go doesn't find by searching. Symptoms: Koolo can't find the character, shows wrong life values, or crashes when reading the game. Check the Koolo community for an updated d2go, then run:

```
go get github.com/hectorgimenez/d2go@<newer version>
```

Then rebuild.
