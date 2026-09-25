//go:build !windows

package service

func terminateWebViewChildren() {}

func SweepOrphanedWebViews(userDataDir string) {}
