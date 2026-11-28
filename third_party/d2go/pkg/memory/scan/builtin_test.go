package scan

import (
	"os"
	"testing"
)

// The pattern table must reproduce exactly the patterns the memory reader used before it became a file.
func TestBuiltinPatternsMatchOriginal(t *testing.T) {
	raw, err := os.ReadFile("../patterns.yaml")
	if err != nil {
		t.Fatal(err)
	}
	set, err := LoadSet(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	orig := map[string][2]string{
		"GameData":          {"\x44\x88\x25\x00\x00\x00\x00\x66\x44\x89\x25\x00\x00\x00\x00", "xxx????xxxx????"},
		"UnitTable":         {"\x48\x03\xC7\x49\x8B\x8C\xC6", "xxxxxxx"},
		"UI":                {"\x40\x84\xed\x0f\x94\x05", "xxxxxx"},
		"Hover":             {"\xc6\x84\xc2\x00\x00\x00\x00\x00\x48\x8b\x74", "xxx?????xxx"},
		"Expansion":         {"\x48\x8B\x05\x00\x00\x00\x00\x48\x8B\xD9\xF3\x0F\x10\x50\x00", "xxx????xxxxxxx?"},
		"Roster":            {"\x02\x45\x33\xD2\x4D\x8B", "xxxxxx"},
		"PanelManager":      {"\x48\x89\x05\x00\x00\x00\x00\x48\x85\xDB\x74\x1E", "xxx????xxxxx"},
		"WidgetStates":      {"\x48\x8B\x0D\x00\x00\x00\x00\x4C\x8D\x44\x24\x00\x48\x03\xC2", "xxx????xxxx?xxx"},
		"Waypoints":         {"\x48\x89\x05\x00\x00\x00\x00\x0F\x11\x00", "xxx????xxx"},
		"FPS":               {"\x8B\x1D\x00\x00\x00\x00\x48\x8D\x05\x00\x00\x00\x00\x48\x8D\x4C\x24\x40", "xx????xxx????xxxxx"},
		"KeyBindings":       {"\x48\x8D\x05\xAF\xEE", "xxxxx"},
		"KeyBindingsSkills": {"\x0F\x10\x04\x24\x48\x6B\xC8\x1C\x48\x8D\x05", "xxxxxxxxxxx"},
		"TerrorZones":       {"\x48\x89\x05\xCC\xCC\xCC\xCC\x48\x8D\x05\xCC\xCC\xCC\xCC\x48\x89\x05\xCC\xCC\xCC\xCC\x48\x8D\x05\xCC\xCC\xCC\xCC\x48\x89\x15\xCC\xCC\xCC\xCC\x48\x89\x15", "xxx????xxx????xxx????xxx????xxx????xxx"},
		"Quests":            {"\x42\xc6\x84\x28\x00\x00\x00\x00\x00\x49\xff\xc5\x49\x83\xfd\x29", "xxxx?????xxxxxxx"},
		"Ping":              {"\x48\x8B\x0D\xCC\xCC\xCC\xCC\x49\x2B\xC7", "xxx????xxx"},
		"LegacyGraphics":    {"\x80\x3D\x00\x00\x00\x00\x00\x48\x8D\x54\x24\x30", "xx?????xxxxx"},
	}
	if len(set) != len(orig) {
		t.Fatalf("%d addresses in patterns.yaml, want %d", len(set), len(orig))
	}
	for name, o := range orig {
		d, ok := set[name]
		if !ok || len(d.Variants) == 0 {
			t.Fatalf("%s missing", name)
		}
		b, mask, err := ParseIDA(d.Variants[0].Pattern)
		if err != nil {
			t.Fatal(err)
		}
		if mask != o[1] {
			t.Errorf("%s mask %s, want %s", name, mask, o[1])
		}
		for i := range b {
			if mask[i] == 'x' && b[i] != o[0][i] {
				t.Errorf("%s byte %d = %02X, want %02X", name, i, b[i], o[0][i])
			}
		}
	}
	for _, n := range []string{"GameData", "UnitTable", "UI", "Hover", "PanelManager"} {
		if !set[n].Essential {
			t.Errorf("%s should be essential", n)
		}
	}
}
