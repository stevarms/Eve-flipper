//go:build !wails

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirIsWritable(t *testing.T) {
	dir := t.TempDir()
	if !dirIsWritable(dir) {
		t.Fatal("a fresh temp dir reported as not writable")
	}
	if dirIsWritable(filepath.Join(dir, "does-not-exist")) {
		t.Fatal("a missing dir reported as writable")
	}
}

// The distroless release image runs the binary out of a read-only
// /usr/local/bin, so the old exe-dir-only rule produced
// "[WARN] file logging init failed: permission denied" and no log file at all.
// $HOME is the mounted /data volume there.
func TestDirIsWritableRejectsAReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not gate directory writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}

	dir := t.TempDir()
	readOnly := filepath.Join(dir, "ro")
	if err := os.Mkdir(readOnly, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })

	if dirIsWritable(readOnly) {
		t.Fatal("a read-only dir reported as writable")
	}
}

func TestResolveLogDirPrefersExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EF_LOG_DIR", dir)

	if got := resolveLogDir(); got != dir {
		t.Fatalf("resolveLogDir() = %q, want the EF_LOG_DIR override %q", got, dir)
	}
}

// `go test` runs from a build-cache temp exe, which resolveLogDir treats the
// same way as `go run` — so it must land on the working directory, not on a
// throwaway folder that disappears with the binary.
func TestResolveLogDirFallsBackToWorkingDirForGoBuiltBinaries(t *testing.T) {
	t.Setenv("EF_LOG_DIR", "")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	got := resolveLogDir()
	if got != cwd {
		t.Fatalf("resolveLogDir() = %q, want the working dir %q", got, cwd)
	}
	if !dirIsWritable(got) {
		t.Fatalf("resolveLogDir() returned %q, which cannot hold a log file", got)
	}
}
