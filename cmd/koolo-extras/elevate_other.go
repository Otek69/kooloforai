//go:build !windows

package main

func isElevated() bool       { return true }
func relaunchElevated() bool { return false }
