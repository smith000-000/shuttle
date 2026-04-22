package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTranscriptLayoutCachePreservedAcrossComposerInput(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil).WithInitialEntries(makeTranscriptEntries(12))
	model.width = 80
	model.height = 24

	width := model.currentTranscriptWidth()
	sentinel := []transcriptRenderLine{{text: "__cached__", entryIndex: 123}}
	model.transcriptLayoutCache = transcriptLayoutCache{
		key: transcriptLayoutCacheKey{
			width:                width,
			contentVersion:       model.transcriptContentVersion,
			selectedEntry:        model.selectedEntry,
			expandedCommandEntry: model.expandedCommandEntry,
		},
		lines: sentinel,
		valid: true,
	}

	nextAny, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next := nextAny.(Model)
	if next.input != "a" {
		t.Fatalf("expected composer input to update, got %q", next.input)
	}

	lines := next.transcriptDisplayLines(width)
	if len(lines) != 1 || lines[0].text != sentinel[0].text || lines[0].entryIndex != sentinel[0].entryIndex {
		t.Fatalf("expected composer input to reuse cached transcript layout, got %#v", lines)
	}
}

func TestTranscriptLayoutCacheInvalidatedOnTranscriptAppend(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil).WithInitialEntries(makeTranscriptEntries(4))
	model.width = 80
	model.height = 24

	width := model.currentTranscriptWidth()
	previousVersion := model.transcriptContentVersion
	model.transcriptLayoutCache = transcriptLayoutCache{
		key: transcriptLayoutCacheKey{
			width:                width,
			contentVersion:       model.transcriptContentVersion,
			selectedEntry:        model.selectedEntry,
			expandedCommandEntry: model.expandedCommandEntry,
		},
		lines: []transcriptRenderLine{{text: "__stale__", entryIndex: 999}},
		valid: true,
	}

	model.appendTranscriptEntries(Entry{Title: "system", Body: "fresh transcript entry"})
	model.syncTranscriptCaches()

	if model.transcriptContentVersion != previousVersion+1 {
		t.Fatalf("expected transcript version to increment from %d to %d, got %d", previousVersion, previousVersion+1, model.transcriptContentVersion)
	}

	lines := model.transcriptDisplayLines(width)
	if len(lines) == 1 && lines[0].text == "__stale__" && lines[0].entryIndex == 999 {
		t.Fatal("expected transcript append to invalidate stale cached lines")
	}
}

func TestTranscriptRenderCachePreservedAcrossComposerInput(t *testing.T) {
	model := NewModel(fakeWorkspace(), nil).WithInitialEntries(makeTranscriptEntries(12))
	model.width = 80
	model.height = 24
	model.syncTranscriptCaches()

	rendered := model.renderTranscript(model.currentTranscriptWidth(), model.currentTranscriptHeight())
	model.transcriptRenderCache.text = "__rendered_cache__"
	model.transcriptRenderCache.valid = true
	if rendered == "__rendered_cache__" {
		t.Fatal("expected initial transcript render to be computed before sentinel swap")
	}

	nextAny, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next := nextAny.(Model)
	got := next.renderTranscript(next.currentTranscriptWidth(), next.currentTranscriptHeight())
	if got != "__rendered_cache__" {
		t.Fatalf("expected composer input to reuse cached rendered transcript, got %q", got)
	}
}
