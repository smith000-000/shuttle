package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExecutableCompletionCandidatesCache(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	pathDir := os.Getenv("PATH")

	gitPath := filepath.Join(pathDir, "git")
	goPath := filepath.Join(pathDir, "go")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(git) error = %v", err)
	}
	if err := os.WriteFile(goPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(go) error = %v", err)
	}

	model := NewModel(fakeWorkspace(), nil)
	first := model.executableCompletionCandidates("g")
	want := []string{"git", "go"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("expected initial executable candidates %v, got %v", want, first)
	}

	if err := os.Remove(goPath); err != nil {
		t.Fatalf("Remove(go) error = %v", err)
	}

	cached := model.executableCompletionCandidates("g")
	if !reflect.DeepEqual(cached, want) {
		t.Fatalf("expected cached executable candidates %v, got %v", want, cached)
	}

	model.shellCompletionCache.executables.loadedAt = model.shellCompletionCache.executables.loadedAt.Add(-executableCompletionCacheTTL - 1)
	refreshed := model.executableCompletionCandidates("g")
	if !reflect.DeepEqual(refreshed, []string{"git"}) {
		t.Fatalf("expected refreshed executable candidates [git], got %v", refreshed)
	}
}

func TestPathCompletionCandidatesCache(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "alpha.txt")
	if err := os.WriteFile(filePath, []byte("alpha"), 0o644); err != nil {
		t.Fatalf("WriteFile(alpha) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("Mkdir(assets) error = %v", err)
	}

	model := NewModel(fakeWorkspace(), nil)
	first := model.pathCompletionCandidates(dir, "a")
	want := []string{"alpha.txt", "assets/"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("expected initial path candidates %v, got %v", want, first)
	}

	if err := os.Remove(filePath); err != nil {
		t.Fatalf("Remove(alpha) error = %v", err)
	}

	cached := model.pathCompletionCandidates(dir, "a")
	if !reflect.DeepEqual(cached, want) {
		t.Fatalf("expected cached path candidates %v, got %v", want, cached)
	}

	model.shellCompletionCache.directories[dir] = directoryCompletionCacheEntry{
		loadedAt: model.shellCompletionCache.directories[dir].loadedAt.Add(-directoryCompletionCacheTTL - 1),
		entries:  model.shellCompletionCache.directories[dir].entries,
	}
	refreshed := model.pathCompletionCandidates(dir, "a")
	if !reflect.DeepEqual(refreshed, []string{"assets/"}) {
		t.Fatalf("expected refreshed path candidates [assets/], got %v", refreshed)
	}
}
