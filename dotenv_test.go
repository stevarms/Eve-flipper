package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMainRootFromGitdir(t *testing.T) {
	main := filepath.Join(string(filepath.Separator)+"projects", "Eve-flipper")
	linked := filepath.Join(main, ".git", "worktrees", "ui-overhaul")

	// Git writes the path with forward slashes even on Windows, which is the
	// form this has to cope with.
	got := mainRootFromGitdir("gitdir: " + filepath.ToSlash(linked) + "\n")
	if got != main {
		t.Fatalf("mainRootFromGitdir(worktree) = %q, want %q", got, main)
	}

	// Native separators and a missing trailing newline are both fine too.
	if got := mainRootFromGitdir("gitdir: " + linked); got != main {
		t.Fatalf("mainRootFromGitdir(native separators) = %q, want %q", got, main)
	}

	notWorktrees := []string{
		// A submodule points into .git/modules; there is no sibling checkout
		// whose .env would be the right one to borrow.
		"gitdir: " + filepath.ToSlash(filepath.Join(main, ".git", "modules", "sub")),
		"gitdir:",
		"gitdir: ",
		"",
		"not a gitdir line at all",
		// A gitdir that is nothing but the marker has no root in front of it.
		"gitdir: " + filepath.ToSlash(filepath.Join(".git", "worktrees", "x")),
	}
	for _, contents := range notWorktrees {
		if got := mainRootFromGitdir(contents); got != "" {
			t.Fatalf("mainRootFromGitdir(%q) = %q, want \"\"", contents, got)
		}
	}
}

func TestMainWorktreeRoot(t *testing.T) {
	base := t.TempDir()
	mainRoot := filepath.Join(base, "repo")
	nested := filepath.Join(mainRoot, "internal", "engine")
	if err := os.MkdirAll(filepath.Join(mainRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	// An ordinary checkout has a .git directory, and its own .env is already
	// the first path loadDotEnv tries.
	if got := mainWorktreeRoot(nested); got != "" {
		t.Fatalf("mainWorktreeRoot(main checkout) = %q, want \"\"", got)
	}

	worktree := filepath.Join(mainRoot, ".claude", "worktrees", "ui-overhaul")
	worktreeNested := filepath.Join(worktree, "internal", "api")
	if err := os.MkdirAll(worktreeNested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitdir := filepath.Join(mainRoot, ".git", "worktrees", "ui-overhaul")
	if err := os.WriteFile(
		filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.ToSlash(gitdir)+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	// The lookup walks up, so it works from anywhere inside the worktree —
	// which matters because `go run ./...` can be issued from a subdirectory.
	for _, dir := range []string{worktree, worktreeNested} {
		if got := mainWorktreeRoot(dir); got != mainRoot {
			t.Fatalf("mainWorktreeRoot(%q) = %q, want %q", dir, got, mainRoot)
		}
	}

	// Nothing git-shaped above it at all.
	outside := t.TempDir()
	if got := mainWorktreeRoot(outside); got != "" {
		t.Fatalf("mainWorktreeRoot(non-repo) = %q, want \"\"", got)
	}
}

func TestLoadDotEnvFallsBackToTheMainCheckout(t *testing.T) {
	// The regression this exists for: .env is gitignored, so `git worktree add`
	// never copies it, and `go run .` puts the binary in a build-cache temp
	// directory. Both of the original lookups therefore miss, and the app
	// reports "SSO not configured" with nothing pointing at the cause.
	base := t.TempDir()
	mainRoot := filepath.Join(base, "repo")
	worktree := filepath.Join(mainRoot, ".claude", "worktrees", "wt")
	if err := os.MkdirAll(filepath.Join(mainRoot, ".git", "worktrees", "wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.ToSlash(filepath.Join(mainRoot, ".git", "worktrees", "wt"))),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(mainRoot, ".env"),
		[]byte("# a comment\n\nEF_DOTENV_TEST_KEY=from-main-checkout\nEF_DOTENV_TEST_PRESET=ignored\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EF_DOTENV_TEST_KEY", "")
	t.Setenv("EF_DOTENV_TEST_PRESET", "already-set")
	t.Chdir(worktree)

	loadDotEnv()

	if got := os.Getenv("EF_DOTENV_TEST_KEY"); got != "from-main-checkout" {
		t.Fatalf("EF_DOTENV_TEST_KEY = %q, want the value from the main checkout's .env", got)
	}
	// A value already in the environment still wins; .env only fills gaps.
	if got := os.Getenv("EF_DOTENV_TEST_PRESET"); got != "already-set" {
		t.Fatalf("EF_DOTENV_TEST_PRESET = %q, want the pre-existing value", got)
	}
}

func TestLoadDotEnvPrefersTheWorkingDirectory(t *testing.T) {
	// The fallback is a last resort: a worktree that does have its own .env
	// must keep using it, or a deliberate per-worktree override would be
	// silently overruled by the main checkout.
	base := t.TempDir()
	mainRoot := filepath.Join(base, "repo")
	worktree := filepath.Join(mainRoot, ".claude", "worktrees", "wt")
	if err := os.MkdirAll(filepath.Join(mainRoot, ".git", "worktrees", "wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.ToSlash(filepath.Join(mainRoot, ".git", "worktrees", "wt"))),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainRoot, ".env"), []byte("EF_DOTENV_TEST_WHICH=main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".env"), []byte("EF_DOTENV_TEST_WHICH=worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EF_DOTENV_TEST_WHICH", "")
	t.Chdir(worktree)

	loadDotEnv()

	if got := os.Getenv("EF_DOTENV_TEST_WHICH"); got != "worktree" {
		t.Fatalf("EF_DOTENV_TEST_WHICH = %q, want the worktree's own .env to win", got)
	}
}
