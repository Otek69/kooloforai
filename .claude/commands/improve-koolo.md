---
description: Review Koolo's recent play and make one measured improvement
---
Goal: make Koolo AI play better or more efficiently. Focus (optional): $ARGUMENTS

1. Collect evidence with the koolo MCP tools: koolo_status (deaths, chickens, errors per game), koolo_logs, and the AI
   brain's memory (lessons, run record).
2. Pick ONE improvement with the clearest payoff, for example a run that keeps failing, a slow step in the logs,
   or bad survival thresholds. Explain why, with the numbers.
3. Implement it (settings or code, with a test for pure logic), rebuild with scripts/dev-rebuild.ps1, then let the bot
   play and compare the same numbers before and after. Keep the change only if it helped.
4. Report the before/after numbers. Commit on a branch; don't push. Offline characters only.
