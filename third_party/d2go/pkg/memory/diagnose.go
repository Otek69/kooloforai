package memory

// kooloforai patch: lets Koolo Doctor check, pattern by pattern, whether this memory reader still
// understands the running D2R build. Patterns are the same ones calculateOffsets uses.

type PatternCheck struct {
	Name    string
	Found   bool
	Address uintptr // offset inside the D2R module
}

type patternDef struct {
	name, pattern, mask string
	byOperand           bool
}

var offsetPatterns = []patternDef{
	{"GameData", "\x44\x88\x25\x00\x00\x00\x00\x66\x44\x89\x25\x00\x00\x00\x00", "xxx????xxxx????", false},
	{"UnitTable", "\x48\x03\xC7\x49\x8B\x8C\xC6", "xxxxxxx", false},
	{"UI", "\x40\x84\xed\x0f\x94\x05", "xxxxxx", false},
	{"Hover", "\xc6\x84\xc2\x00\x00\x00\x00\x00\x48\x8b\x74", "xxx?????xxx", false},
	{"Expansion", "\x48\x8B\x05\x00\x00\x00\x00\x48\x8B\xD9\xF3\x0F\x10\x50\x00", "xxx????xxxxxxx?", false},
	{"Roster", "\x02\x45\x33\xD2\x4D\x8B", "xxxxxx", false},
	{"PanelManager", "\x48\x89\x05\x00\x00\x00\x00\x48\x85\xDB\x74\x1E", "xxx????xxxxx", true},
	{"WidgetStates", "\x48\x8B\x0D\x00\x00\x00\x00\x4C\x8D\x44\x24\x00\x48\x03\xC2", "xxx????xxxx?xxx", false},
	{"Waypoints", "\x48\x89\x05\x00\x00\x00\x00\x0F\x11\x00", "xxx????xxx", false},
	{"FPS", "\x8B\x1D\x00\x00\x00\x00\x48\x8D\x05\x00\x00\x00\x00\x48\x8D\x4C\x24\x40", "xx????xxx????xxxxx", false},
	{"KeyBindings", "\x48\x8D\x05\xAF\xEE", "xxxxx", false},
	{"KeyBindingsSkills", "\x0F\x10\x04\x24\x48\x6B\xC8\x1C\x48\x8D\x05", "xxxxxxxxxxx", false},
	{"TerrorZones", "\x48\x89\x05\xCC\xCC\xCC\xCC\x48\x8D\x05\xCC\xCC\xCC\xCC\x48\x89\x05\xCC\xCC\xCC\xCC\x48\x8D\x05\xCC\xCC\xCC\xCC\x48\x89\x15\xCC\xCC\xCC\xCC\x48\x89\x15", "xxx????xxx????xxx????xxx????xxx????xxx", false},
	{"Quests", "\x42\xc6\x84\x28\x00\x00\x00\x00\x00\x49\xff\xc5\x49\x83\xfd\x29", "xxxx?????xxxxxxx", false},
	{"Ping", "\x48\x8B\x0D\xCC\xCC\xCC\xCC\x49\x2B\xC7", "xxx????xxx", false},
	{"LegacyGraphics", "\x80\x3D\x00\x00\x00\x00\x00\x48\x8D\x54\x24\x30", "xx?????xxxxx", false},
}

// DiagnosePatterns scans the game module and reports which offset patterns are found.
// The first 5 (GameData, UnitTable, UI, Hover, Expansion) are essential for Koolo.
func (p *Process) DiagnosePatterns() ([]PatternCheck, error) {
	memory, err := p.getProcessMemory()
	if err != nil {
		return nil, err
	}
	out := make([]PatternCheck, 0, len(offsetPatterns))
	for _, d := range offsetPatterns {
		off := p.findPattern(memory, d.pattern, d.mask)
		out = append(out, PatternCheck{Name: d.name, Found: off != 0, Address: uintptr(off)})
	}
	return out, nil
}
