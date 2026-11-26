package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/hectorgimenez/koolo/internal/config"
)

func hasFlag(name string) bool {
	for _, a := range os.Args[1:] {
		if a == name {
			return true
		}
	}
	return false
}

// writeCrashReport saves what we know about a crash next to the logs, so it can be attached to a bug report
// (Koolo Doctor includes these in its debug bundle).
func writeCrashReport(r any, stack []byte) string {
	dir := "crash-reports"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "crash-"+time.Now().Format("2006-01-02-15-04-05")+".txt")

	bi, _ := debug.ReadBuildInfo()
	d2go := "unknown"
	if bi != nil {
		for _, dep := range bi.Deps {
			if dep.Path == "github.com/hectorgimenez/d2go" {
				d2go = dep.Version
			}
		}
	}
	all := make([]byte, 1<<20)
	all = all[:runtime.Stack(all, true)]

	report := fmt.Sprintf("Koolo AI crash report\nTime: %s\nVersion: %s\nd2go: %s\nGo: %s %s/%s\n\nPanic: %v\n\nStack:\n%s\n\nAll goroutines:\n%s\n",
		time.Now().Format(time.RFC3339), config.Version, d2go, runtime.Version(), runtime.GOOS, runtime.GOARCH, r, stack, all)
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return ""
	}
	abs, _ := filepath.Abs(path)
	return abs
}
