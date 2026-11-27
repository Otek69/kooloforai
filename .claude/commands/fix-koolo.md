---
description: Diagnose and fix a problem in the running Koolo AI bot
---
Something is wrong with Koolo AI. Extra details from the user: $ARGUMENTS

Follow the "Fix loop" in CLAUDE.md:
1. Diagnose with the koolo MCP tools: koolo_doctor, koolo_logs (errorsOnly), koolo_crash_reports, koolo_status,
   and koolo_screenshot if useful. Summarise the root cause in two or three sentences before changing anything.
2. Fix it with a settings change if that is enough, otherwise with the smallest code change (plus a test where possible).
3. Rebuild and restart with scripts/dev-rebuild.ps1, start the character with koolo_control, and verify with
   koolo_logs and koolo_status that the problem is gone.
4. Report what was wrong, what you changed and how you verified it. Commit code changes on a branch; don't push.
Offline characters only.
