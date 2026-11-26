package brain

import _ "embed"

// playbook is the route knowledge distilled from MrLlamaSC's speedrun tutorials and speedrun.com/d2lod.
// Diablo II: Resurrected uses the same maps, quests and mechanics, so it applies to Koolo too.
//
//go:embed playbook.md
var playbook string
