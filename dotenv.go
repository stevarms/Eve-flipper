package main

import (
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnv loads environment variables from a local .env file so that
// double-clicked binaries (without a shell) can still use ESI_* settings.
// Order of lookup:
//  1. ./.env (current working directory)
//  2. <binary-dir>/.env
//  3. <main-checkout>/.env, when the working directory is inside a linked
//     git worktree — see mainWorktreeRoot.
//
// Existing OS env vars are NOT overridden.
//
// Shared by the web and Wails entry points, which are mutually exclusive
// builds. It used to be copied into both; the copies had already started to
// drift, and this is the file that decides whether SSO works at all.
func loadDotEnv() {
	paths := []string{".env"}

	if exePath, err := os.Executable(); err == nil {
		if exeDir := filepath.Dir(exePath); exeDir != "" {
			paths = append(paths, filepath.Join(exeDir, ".env"))
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		if root := mainWorktreeRoot(cwd); root != "" {
			paths = append(paths, filepath.Join(root, ".env"))
		}
	}

	seen := make(map[string]bool)

	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true

		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			l := strings.TrimSpace(line)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			parts := strings.SplitN(l, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key == "" {
				continue
			}
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, val)
			}
		}
	}
}

// mainWorktreeRoot returns the top-level checkout that dir belongs to when dir
// sits inside a *linked* git worktree, and "" in every other case — including
// the ordinary checkout, where the first two lookups already do the job.
//
// .env is gitignored, so `git worktree add` never copies it. Running
// `go run .` from a worktree therefore finds no credentials at all: the
// working directory has no .env, and the binary lives in a Go build-cache temp
// directory. What that looks like from the outside is the app reporting "SSO
// not configured" with no hint as to why, which is a long way from the cause.
//
// A linked worktree's .git is a file reading `gitdir: <main>/.git/worktrees/<name>`,
// so the main checkout is recoverable from it without shelling out to git.
func mainWorktreeRoot(dir string) string {
	for {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			if info.IsDir() {
				// The main checkout. Its own .env, if any, was already the
				// first path tried.
				return ""
			}
			data, readErr := os.ReadFile(gitPath)
			if readErr != nil {
				return ""
			}
			return mainRootFromGitdir(string(data))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func mainRootFromGitdir(contents string) string {
	line, _, _ := strings.Cut(contents, "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return ""
	}
	// Git writes forward slashes here even on Windows.
	gitdir := filepath.FromSlash(strings.TrimSpace(rest))
	if gitdir == "" {
		return ""
	}
	marker := string(filepath.Separator) + filepath.Join(".git", "worktrees") + string(filepath.Separator)
	idx := strings.Index(gitdir, marker)
	if idx <= 0 {
		// A submodule's gitdir points at .git/modules/... instead; there is no
		// sibling checkout to borrow a .env from.
		return ""
	}
	return filepath.Clean(gitdir[:idx])
}
