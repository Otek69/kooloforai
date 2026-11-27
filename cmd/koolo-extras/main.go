// koolo-extras: optional features that run NEXT TO Koolo, in their own process.
// Nothing here can crash Koolo: the extras only talk to it through Koolo's MCP server.
//
//	koolo-extras.exe              run all enabled extras (config/extras.yaml)
//	koolo-extras.exe --once-check check config and the connection to Koolo, then exit
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/hectorgimenez/koolo/internal/config"
	"github.com/hectorgimenez/koolo/internal/extras"
)

func main() {
	exe, _ := os.Executable()
	home := filepath.Dir(exe)
	_ = os.Chdir(home)
	if applyUpdate(exe) {
		return // the new version was started
	}

	// Restarting Koolo needs administrator rights (Koolo reads game memory), so ask once at start.
	if !hasArg("--no-elevate") && !isElevated() && relaunchElevated() {
		return
	}

	cfg, err := extras.LoadConfig(filepath.Join("config", "extras.yaml"))
	if err != nil {
		fmt.Println("Could not read config/extras.yaml:", err)
		waitEnter()
		return
	}
	if cfg.InstallDir == "" {
		cfg.InstallDir = home
	}

	_ = os.MkdirAll(filepath.Join("extras", "logs"), 0o755)
	logFile, _ := os.Create(filepath.Join("extras", "logs", "extras-"+time.Now().Format("2006-01-02-15-04-05")+".txt"))
	var out io.Writer = os.Stdout
	if logFile != nil {
		defer logFile.Close()
		out = io.MultiWriter(os.Stdout, logFile)
	}
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))

	env := &extras.Env{
		Cfg:    cfg,
		Koolo:  &extras.MCPKoolo{URL: cfg.KooloURL},
		Runner: extras.ExecRunner{},
		Bus:    extras.NewBus(),
		Store:  &extras.Store{Dir: filepath.Join("extras", "data")},
		Log:    logger,
		Now:    time.Now,
	}

	if hasArg("--once-check") {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if st, err := extras.GetStatus(ctx, env.Koolo); err != nil {
			fmt.Println("Koolo:", err)
		} else {
			fmt.Printf("Koolo %s reachable, characters: %d\n", st.Version, len(st.Characters))
		}
		waitEnter()
		return
	}

	sup := extras.NewSupervisor(env)
	modules := []extras.Module{&extras.Dashboard{Sup: sup}, &extras.Alerts{}}
	enabled := []string{"dashboard", "alerts"}
	add := func(on bool, m extras.Module) {
		if on {
			modules = append(modules, m)
			enabled = append(enabled, m.Name())
		}
	}
	add(cfg.Stats.Enabled, &extras.Stats{})
	add(cfg.Watchdog.Enabled, &extras.Watchdog{})
	add(cfg.Autopilot.Enabled, &extras.Autopilot{})
	add(cfg.PatchWatch.Enabled, &extras.PatchWatch{})
	if cfg.Session.Enabled {
		s := &extras.Session{}
		if cfg.Session.OBS.Enabled {
			s.OBS = &extras.OBSRecorder{Host: cfg.Session.OBS.Host, Port: cfg.Session.OBS.Port, Password: cfg.Session.OBS.Password}
		}
		add(true, s)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var wg sync.WaitGroup
	sup.Start(ctx, modules, &wg)

	fmt.Printf("Koolo AI Extras %s running: %v\n", config.Version, enabled)
	fmt.Printf("Dashboard: http://localhost:%d/   OBS banner: http://localhost:%d/overlay\n", cfg.DashboardPort, cfg.DashboardPort)
	fmt.Println("Koolo keeps running even if an extra fails. Close this window or press Ctrl+C to stop the extras.")
	env.Bus.Publish(extras.Event{Kind: extras.EvAlert, Text: fmt.Sprintf("Koolo AI Extras started: %v", enabled)})
	<-ctx.Done()
	wg.Wait()
}

// applyUpdate swaps in koolo-extras.new.exe (built by scripts/dev-rebuild.ps1) and starts it.
// Windows can't overwrite a running exe, but it can rename it.
func applyUpdate(exe string) bool {
	dir := filepath.Dir(exe)
	old := filepath.Join(dir, "koolo-extras.old.exe")
	_ = os.Remove(old) // leftover from the previous update
	newExe := filepath.Join(dir, "koolo-extras.new.exe")
	if _, err := os.Stat(newExe); err != nil {
		return false
	}
	if err := os.Rename(exe, old); err != nil {
		return false
	}
	if err := os.Rename(newExe, exe); err != nil {
		_ = os.Rename(old, exe) // put the working version back
		return false
	}
	fmt.Println("Updated koolo-extras, restarting...")
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Start() == nil
}

func hasArg(a string) bool {
	for _, x := range os.Args[1:] {
		if x == a {
			return true
		}
	}
	return false
}

func waitEnter() {
	fmt.Print("Press Enter to close...")
	_, _ = fmt.Scanln()
}
