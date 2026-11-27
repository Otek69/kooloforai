// Koolo Doctor: checks everything Koolo AI needs and builds a debug bundle you can send for help.
//
//	koolo-doctor.exe            run all checks, save diagnostics\report-<time>.txt
//	koolo-doctor.exe --bundle   checks + a zip with logs, crash reports and configs (secrets removed)
package main

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/hectorgimenez/d2go/pkg/data/stat"
	"github.com/hectorgimenez/d2go/pkg/memory"
	"github.com/hectorgimenez/d2go/pkg/nip"
	"github.com/hectorgimenez/koolo/internal/brain"
	"github.com/hectorgimenez/koolo/internal/config"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"gopkg.in/yaml.v3"
)

type status int

const (
	ok status = iota
	warn
	fail
	info
)

var (
	report   strings.Builder
	problems int
	warnings int
)

func line(s status, area, msg string) {
	tag := map[status]string{ok: "[ OK ]", warn: "[WARN]", fail: "[FAIL]", info: "[INFO]"}[s]
	text := fmt.Sprintf("%s %-12s %s", tag, area, msg)
	fmt.Println(text)
	report.WriteString(text + "\n")
	switch s {
	case fail:
		problems++
	case warn:
		warnings++
	}
}

func section(title string) {
	t := "\n== " + title + " =="
	fmt.Println(t)
	report.WriteString(t + "\n")
}

func main() {
	bundle := hasArg("--bundle")

	// Reading game memory needs administrator rights (Koolo itself runs elevated), so ask for them once.
	if !windows.GetCurrentProcessToken().IsElevated() && !hasArg("--no-elevate") {
		if relaunchElevated() {
			return
		}
	}

	// Always work from the folder Koolo lives in, wherever the shortcut starts us.
	if exe, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exe))
	}

	fmt.Println("Koolo Doctor - checking your Koolo AI setup...")
	report.WriteString("Koolo Doctor report " + time.Now().Format(time.RFC3339) + "\n")

	checkSystem()
	kooloCfg := checkKooloConfig()
	checkCharacters()
	checkBrain()
	checkGame()
	checkLogs(kooloCfg.LogSaveDirectory)

	section("Summary")
	switch {
	case problems > 0:
		line(fail, "Result", fmt.Sprintf("%d problem(s), %d warning(s). Fix the [FAIL] lines first.", problems, warnings))
	case warnings > 0:
		line(warn, "Result", fmt.Sprintf("No blocking problems, %d warning(s).", warnings))
	default:
		line(ok, "Result", "Everything looks good.")
	}

	_ = os.MkdirAll("diagnostics", 0o755)
	stamp := time.Now().Format("2006-01-02-15-04-05")
	reportPath := filepath.Join("diagnostics", "report-"+stamp+".txt")
	_ = os.WriteFile(reportPath, []byte(report.String()), 0o644)
	abs, _ := filepath.Abs(reportPath)
	fmt.Println("\nReport saved to", abs)

	if bundle {
		zipPath := filepath.Join("diagnostics", "koolo-debug-"+stamp+".zip")
		if err := makeBundle(zipPath, kooloCfg.LogSaveDirectory); err != nil {
			fmt.Println("Could not create the debug bundle:", err)
		} else {
			absZip, _ := filepath.Abs(zipPath)
			fmt.Println("Debug bundle saved to", absZip)
			fmt.Println("It contains logs, crash reports and configs with API keys, tokens and passwords removed.")
			if !hasArg("--no-wait") {
				_ = exec.Command("explorer", "/select,", absZip).Start()
			}
		}
	}

	if !hasArg("--no-wait") {
		fmt.Print("\nPress Enter to close...")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

// ---------------------------------------------------------------------------

func hasArg(a string) bool {
	for _, x := range os.Args[1:] {
		if x == a {
			return true
		}
	}
	return false
}

// relaunchElevated restarts the doctor with a UAC prompt. Returns false if the user said no.
func relaunchElevated() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	args := strings.Join(append(os.Args[1:], "--no-elevate"), " ")
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(args)
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_SHOWNORMAL) == nil
}

func checkSystem() {
	section("System")
	line(info, "Koolo", "version "+config.Version)
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/hectorgimenez/d2go" {
				line(info, "d2go", d.Version+" (the memory reader; must match your D2R patch)")
			}
		}
	}
	line(info, "Windows", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))
	if windows.GetCurrentProcessToken().IsElevated() {
		line(ok, "Admin", "running as administrator (Koolo needs this to read the game)")
	} else {
		line(warn, "Admin", "not running as administrator: the game memory test below may fail. Right-click Koolo Doctor > Run as administrator")
	}
}

type kooloYAML struct {
	D2RPath          string `yaml:"D2RPath"`
	D2LoDPath        string `yaml:"D2LoDPath"`
	LogSaveDirectory string `yaml:"logSaveDirectory"`
}

func checkKooloConfig() kooloYAML {
	section("Koolo settings")
	var k kooloYAML
	raw, err := os.ReadFile(filepath.Join("config", "koolo.yaml"))
	if err != nil {
		line(fail, "koolo.yaml", "config\\koolo.yaml not found: start Koolo once and finish the first-run settings")
		return k
	}
	if err := yaml.Unmarshal(raw, &k); err != nil {
		line(fail, "koolo.yaml", "can't be read: "+err.Error())
		return k
	}
	line(ok, "koolo.yaml", "found and readable")

	checkDir := func(name, dir string, wantFiles ...string) {
		if dir == "" {
			line(fail, name, "path is empty in Koolo settings")
			return
		}
		if _, err := os.Stat(dir); err != nil {
			line(fail, name, dir+" does not exist")
			return
		}
		for _, f := range wantFiles {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				line(ok, name, dir)
				return
			}
		}
		line(warn, name, fmt.Sprintf("%s exists but none of %v were found in it", dir, wantFiles))
	}
	checkDir("D2R", k.D2RPath, "D2R.exe")
	checkDir("D2 LoD 1.13c", k.D2LoDPath, "Game.exe", "Diablo II.exe", "d2data.mpq")
	return k
}

func checkCharacters() {
	section("Characters")
	entries, err := os.ReadDir("config")
	if err != nil {
		line(fail, "Characters", "config folder not found")
		return
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "template" {
			continue
		}
		cfgFile := filepath.Join("config", e.Name(), "config.yaml")
		raw, err := os.ReadFile(cfgFile)
		if err != nil {
			continue
		}
		found++
		var c config.CharacterCfg
		if err := yaml.Unmarshal(raw, &c); err != nil {
			line(fail, e.Name(), "config.yaml can't be read: "+err.Error())
			continue
		}
		runs := make([]string, 0, len(c.Game.Runs))
		for _, r := range c.Game.Runs {
			runs = append(runs, string(r))
		}
		line(ok, e.Name(), fmt.Sprintf("class %s, runs: %s", c.Character.Class, strings.Join(runs, ", ")))

		pickit := filepath.Join("config", e.Name(), "pickit")
		rules, err := nip.ReadDir(pickit + string(os.PathSeparator))
		switch {
		case err != nil:
			line(fail, e.Name(), "pickit rules have an error: "+err.Error())
		case len(rules) == 0:
			line(warn, e.Name(), "no pickit rules: Koolo won't pick up anything")
		default:
			line(ok, e.Name(), fmt.Sprintf("%d pickit rules load fine", len(rules)))
		}
	}
	if found == 0 {
		line(warn, "Characters", "no characters set up yet (add one in Koolo's dashboard)")
	}
}

func checkBrain() {
	section("AI brain")
	raw, err := os.ReadFile(filepath.Join("config", "brain.yaml"))
	if err != nil {
		line(info, "brain.yaml", "not found: the AI brain is off (Koolo works normally)")
		return
	}
	var c brain.Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		line(fail, "brain.yaml", "can't be read: "+err.Error())
		return
	}
	if !c.Enabled {
		line(info, "brain.yaml", "enabled: false (the AI brain is off)")
		return
	}
	provider := c.Provider
	if provider == "" {
		provider = "anthropic"
	}
	line(ok, "brain.yaml", fmt.Sprintf("enabled, provider %s, smart model %s, fast model %s", provider, c.SmartModel, c.FastModel))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if provider == "desktop" {
		line(ok, "Claude Desktop", "desktop mode: Claude Desktop is the brain via the koolo MCP server (no API key needed). "+
			"Koolo serves it at http://localhost:8087/mcp while it runs")
		return
	}
	if provider == "local" {
		url := strings.TrimRight(c.LocalURL, "/")
		if url == "" {
			url = "http://localhost:11434/v1"
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/models", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			line(fail, "Local model", "no model server at "+url+": start Ollama or LM Studio")
			return
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			line(fail, "Local model", fmt.Sprintf("%s answered %d", url, resp.StatusCode))
			return
		}
		line(ok, "Local model", "server reachable at "+url)
		for _, m := range []string{c.SmartModel, c.FastModel} {
			if m != "" && !strings.Contains(string(body), m) {
				line(warn, "Local model", fmt.Sprintf("model %q is not downloaded yet (ollama pull %s)", m, m))
			}
		}
		return
	}

	key := c.APIKey
	source := "brain.yaml"
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
		source = "ANTHROPIC_API_KEY"
	}
	if key == "" {
		line(fail, "Claude API", "no API key: set ANTHROPIC_API_KEY or apiKey in brain.yaml (or use provider: local)")
		return
	}
	client := anthropic.NewClient(option.WithAPIKey(key), option.WithMaxRetries(1))
	if _, err := client.Models.List(ctx, anthropic.ModelListParams{}); err != nil {
		line(fail, "Claude API", "the key from "+source+" was rejected or the API is unreachable: "+shorten(err.Error(), 160))
		return
	}
	line(ok, "Claude API", "key from "+source+" works")
}

func checkGame() {
	section("Game (Diablo II: Resurrected)")
	// Find D2R by its process, not by window title: Koolo renames the game window ("D2R - [pid] - name - ...").
	proc, err := memory.NewProcess()
	if err != nil {
		line(warn, "Game", "D2R is not running (or can't be opened as administrator). Start it windowed, enter a game, and run Koolo Doctor again")
		return
	}
	defer proc.Close()
	checkWindow(proc.GetPID())
	base, size := proc.ModuleInfo()
	line(info, "Memory", fmt.Sprintf("D2R module at 0x%X, %d MB", base, size>>20))

	// Step 1: can we read the game's code, and do the address patterns still match this D2R build?
	checks, err := safeDiagnose(proc)
	if err != nil {
		line(fail, "Memory", err.Error())
		return
	}
	bad, total := proc.MemoryHealth()
	if bad > 0 {
		line(info, "Memory", fmt.Sprintf("%d of %d pages unreadable (skipped; normal on newer D2R builds)", bad, total))
	}
	var missing []string
	essentialMissing := false
	for i, c := range checks {
		if !c.Found {
			missing = append(missing, c.Name)
			if i < 5 {
				essentialMissing = true
			}
		}
	}
	switch {
	case len(missing) == 0:
		line(ok, "Patterns", fmt.Sprintf("all %d address patterns found in this D2R build", len(checks)))
	case essentialMissing:
		line(fail, "Patterns", fmt.Sprintf("essential patterns missing: %s. This D2R patch is newer than the memory reader: d2go needs an update", strings.Join(missing, ", ")))
		return
	default:
		line(warn, "Patterns", "some optional patterns missing: "+strings.Join(missing, ", ")+" (related features may not work)")
	}

	// Step 2: read the live game.
	defer func() {
		if r := recover(); r != nil {
			line(fail, "Memory", fmt.Sprintf("reading the game crashed: %v", r))
			for _, l := range strings.Split(string(debug.Stack()), "\n") {
				if strings.Contains(l, "d2go") || strings.Contains(l, "koolo") {
					line(info, "", strings.TrimSpace(l))
				}
			}
		}
	}()
	gr := memory.NewGameReader(proc)
	if !gr.InGame() {
		line(warn, "Memory", "D2R found, but you're not in a game. Enter a game (offline character) and run again to test fully")
		return
	}
	d := gr.GetData()
	pu := d.PlayerUnit
	lvl, _ := pu.FindStat(stat.Level, 0)
	life, _ := pu.FindStat(stat.Life, 0)
	maxLife, _ := pu.FindStat(stat.MaxLife, 0)
	line(info, "Memory", fmt.Sprintf("name %q, level %d, area %s, life %d/%d, position %d,%d, %d monsters, %d items",
		pu.Name, lvl.Value, pu.Area.Area().Name, life.Value, maxLife.Value, pu.Position.X, pu.Position.Y,
		len(d.Monsters), len(d.Inventory.AllItems)))

	sane := pu.Name != "" && lvl.Value >= 1 && lvl.Value <= 99 && maxLife.Value > 0 && pu.Area.Area().Name != ""
	if sane {
		line(ok, "Memory", "game data looks correct: Koolo can read this D2R version")
	} else {
		line(fail, "Memory", "game data looks wrong: the memory reader likely needs an update for this D2R patch (newer d2go)")
	}
}

var procGetWindowTextW = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowTextW")

// checkWindow finds the D2R window of the given process and checks its size.
// (The title shows the Battle.net region the launcher is logged into, even for offline characters.)
func checkWindow(pid uint32) {
	var found windows.HWND
	cb := syscall.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		var wpid uint32
		_, _ = windows.GetWindowThreadProcessId(h, &wpid)
		if wpid == pid && windows.IsWindowVisible(h) {
			found = h
			return 0 // stop
		}
		return 1
	})
	_ = windows.EnumWindows(cb, nil)
	if found == 0 {
		line(warn, "Window", "D2R process found but no visible window")
		return
	}
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(uintptr(found), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	title := windows.UTF16ToString(buf[:n])
	line(info, "Window", fmt.Sprintf("%q", title))

	var rc win.RECT
	win.GetClientRect(win.HWND(found), &rc)
	w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
	if (w == 1280 && h == 720) || (w == 1920 && h == 1080) {
		line(ok, "Window", fmt.Sprintf("%dx%d", w, h))
	} else {
		line(warn, "Window", fmt.Sprintf("%dx%d: Koolo expects windowed 1280x720 (or 1920x1080 in HiDPI mode)", w, h))
	}
}

// safeDiagnose runs the pattern scan, turning a crash into an error with the location.
func safeDiagnose(proc *memory.Process) (checks []memory.PatternCheck, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("scanning the game's memory crashed: %v\n%s", r, debug.Stack())
		}
	}()
	return proc.DiagnosePatterns()
}

func checkLogs(dir string) {
	section("Recent logs")
	if dir == "" {
		dir = "logs"
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	if len(files) == 0 {
		line(info, "Logs", "no log files yet")
		return
	}
	sort.Slice(files, func(i, j int) bool { return modTime(files[i]).After(modTime(files[j])) })
	latest := files[0]
	line(info, "Logs", fmt.Sprintf("latest: %s (%s)", latest, modTime(latest).Format("2006-01-02 15:04")))
	f, err := os.Open(latest)
	if err != nil {
		return
	}
	defer f.Close()
	var errs []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "level=ERROR") {
			errs = append(errs, sc.Text())
		}
	}
	if len(errs) == 0 {
		line(ok, "Logs", "no errors in the latest log")
		return
	}
	line(warn, "Logs", fmt.Sprintf("%d error lines in the latest log, last ones:", len(errs)))
	for _, e := range errs[max(0, len(errs)-5):] {
		line(info, "", shorten(e, 220))
	}
	crashes, _ := filepath.Glob(filepath.Join("crash-reports", "*.txt"))
	if len(crashes) > 0 {
		line(warn, "Crashes", fmt.Sprintf("%d crash report(s) in crash-reports\\", len(crashes)))
	}
}

// ---------------------------------------------------------------------------

var secretLine = regexp.MustCompile(`(?i)^(\s*[\w.-]*(api[_-]?key|token|password|secret|webhook|username|email|chatid)[\w.-]*\s*:\s*).+$`)

func redact(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if secretLine.MatchString(l) {
			lines[i] = secretLine.ReplaceAllString(l, `${1}"<removed>"`)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func makeBundle(zipPath, logDir string) error {
	if logDir == "" {
		logDir = "logs"
	}
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()

	add := func(name string, data []byte) {
		if w, err := zw.Create(filepath.ToSlash(name)); err == nil {
			_, _ = w.Write(data)
		}
	}
	addFile := func(path string, redactIt bool, maxBytes int64) {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		if st, err := f.Stat(); err == nil && st.Size() > maxBytes {
			_, _ = f.Seek(st.Size()-maxBytes, io.SeekStart) // keep the end of big files
		}
		data, _ := io.ReadAll(f)
		if redactIt {
			data = redact(data)
		}
		add(path, data)
	}
	newest := func(pattern string, n int) []string {
		files, _ := filepath.Glob(pattern)
		sort.Slice(files, func(i, j int) bool { return modTime(files[i]).After(modTime(files[j])) })
		return files[:min(n, len(files))]
	}

	add("report.txt", []byte(report.String()))
	for _, f := range newest(filepath.Join(logDir, "*.txt"), 5) {
		addFile(f, true, 8<<20)
	}
	for _, f := range newest(filepath.Join("crash-reports", "*.txt"), 5) {
		addFile(f, false, 4<<20)
	}
	for _, f := range newest(filepath.Join("screenshots", "*.jpeg"), 5) {
		addFile(f, false, 4<<20)
	}
	cfgs, _ := filepath.Glob(filepath.Join("config", "*.yaml"))
	chars, _ := filepath.Glob(filepath.Join("config", "*", "config.yaml"))
	for _, f := range append(cfgs, chars...) {
		addFile(f, true, 1<<20)
	}
	addFile(filepath.Join("brain", "memory.json"), false, 1<<20)
	addFile(filepath.Join("brain", "thoughts.jsonl"), false, 1<<20)
	return nil
}

func modTime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
