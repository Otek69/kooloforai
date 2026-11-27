# Claude Desktop as Koolo's brain

With `provider: desktop`, **the Claude Desktop app is the brain**. It uses your Claude plan: no API key and no per-call cost.

**How it works:**
- Koolo plays by itself all the time. It records every new game, run, item, death and chicken.
- When you talk to Claude Desktop, it reads all of that through Koolo's MCP server. It looks at death screenshots, decides, and **sets the plan**: run order, goal, survival thresholds. It also writes lessons and **commentary for the stream overlay**.
- Koolo follows the latest plan from the next game on, until Claude sets a new one.

Claude Desktop only acts when you send it a message; Koolo can't call it by itself. For a brain that decides automatically every game, use `provider: anthropic` (API) or `provider: local` (Ollama).

## Setup

**1. Koolo:** in `config\brain.yaml`, set:

```yaml
enabled: true
provider: desktop
```

Then (re)start Koolo AI. Koolo Doctor should show `[ OK ] Claude Desktop`.

**2. Node.js:** install it from https://nodejs.org (the LTS version). Claude Desktop needs it to connect to Koolo's local MCP server.

**3. Connect Claude Desktop:** open **Settings → Developer → Edit Config**. That opens `%APPDATA%\Claude\claude_desktop_config.json`. Add:

```json
{
  "mcpServers": {
    "koolo": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "http://localhost:8087/mcp"]
    }
  }
}
```

If the file already has `mcpServers`, add only the `"koolo": {...}` entry. Restart Claude Desktop. The tools icon should then list the `koolo_...` tools. Koolo must be running for them to connect.

**4. Create a Project in Claude Desktop** called "Koolo Brain":
- **Instructions:** paste the block below.
- **Project knowledge:** add `brain-playbook.md` from your Koolo AI folder (`%LOCALAPPDATA%\Programs\KooloAI`). It holds the speedrun route knowledge.

```
You are the brain of Koolo AI, a bot playing Diablo II: Resurrected with an OFFLINE character, live for a
YouTube channel. Koolo does all the real-time playing; you make the decisions and talk to the viewers.

Every time I write to you (even just "check"):
1. Read koolo_events since the last event id you saw (start with 0), and koolo_status.
2. If there was a death or chicken, look at koolo_last_failure and save one concrete lesson with koolo_add_lesson.
3. Decide the plan for the next games using the run record, lessons, level and gear (koolo_game_state), and the
   route playbook in this project. Set it with koolo_brain_plan, including a short "thought" for the viewers.
4. React to big moments (rare items, level ups, deaths) with koolo_say: 1-2 short, entertaining sentences.
5. Tell me in 3-5 lines what happened and what you decided. Remember the last event id.
Never start, stop or change settings beyond the plan unless I ask. Offline characters only.
```

## Use

While Koolo plays, write to Claude Desktop in the Project:
- **"check"**: it reviews what happened, updates the plan and comments for the overlay.
- **"the sorc keeps dying in the Pit, why?"**: it looks at the deaths and screenshots and adjusts the plan.
- **"what should we farm next?"**: it plans from your gear and the speedrun knowledge.

To make it more automatic, keep the conversation open and write "check" every few games. Long sessions use your plan's usage limits; each "check" is a small request.

## Brain tools for Claude

| Tool | Purpose |
|---|---|
| `koolo_events` | What happened since the last check: games, runs, items, deaths, chickens |
| `koolo_status` | Stats per character, plus the brain's goal, lessons and run record |
| `koolo_game_state` / `koolo_screenshot` | Live level, gear, stash, and the screen |
| `koolo_last_failure` | Screenshot and description of the last death or chicken |
| `koolo_brain_plan` | Set run order, goal and survival for the next games |
| `koolo_add_lesson` | Save a lesson to long-term memory |
| `koolo_say` | Commentary on the stream overlay |

Plus the debugging tools (`koolo_doctor`, `koolo_logs`, and so on); see DEBUGGING.md.
