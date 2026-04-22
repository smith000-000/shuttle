package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"aiterm/internal/controller"
	"aiterm/internal/provider"
	"aiterm/internal/shell"

	tea "github.com/charmbracelet/bubbletea"
)

func benchmarkTranscriptEntries(count int) []Entry {
	entries := make([]Entry, 0, count)
	for index := 0; index < count; index++ {
		command := fmt.Sprintf("go test ./pkg/%03d -run TestVeryLongCaseNameWithSelectionHintsAndFormatting", index)
		body := strings.Join([]string{
			fmt.Sprintf("line %03d: starting work in /Users/jsmith/source/shuttle", index),
			"stdout: building package and preparing diagnostics for render performance",
			"stderr: warning: synthetic benchmark payload to exercise transcript wrapping",
			"summary: the command completed successfully and produced several wrapped lines of output",
		}, "\n")
		entries = append(entries, Entry{
			Title:   "result",
			Command: command,
			Body:    body,
			Detail:  body,
		})
	}
	return entries
}

func benchmarkRenderModel() Model {
	ctrl := &fakeController{
		contextUsage: controller.ContextWindowUsage{ApproxPromptTokens: 64000},
	}
	model := NewModel(fakeWorkspace(), ctrl)
	model.width = 120
	model.height = 40
	model.mode = ShellMode
	model.shellContext = shell.PromptContext{
		User:      "jsmith",
		Host:      "workstation",
		Directory: "/Users/jsmith/source/shuttle",
	}
	model.activeProvider = provider.Profile{
		Model: "gpt-5.4-mini",
		SelectedModel: &provider.ModelOption{
			ID:            "gpt-5.4-mini",
			Name:          "GPT-5.4 Mini",
			ContextWindow: 256000,
		},
	}
	model = model.WithInitialEntries(benchmarkTranscriptEntries(250))
	model.input = "git status"
	model.cursor = len([]rune(model.input))
	model.recomputeCompletion()
	return model
}

func BenchmarkModelViewShell(b *testing.B) {
	model := benchmarkRenderModel()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.View()
	}
}

func BenchmarkRenderTranscriptHot(b *testing.B) {
	model := benchmarkRenderModel()
	width := model.currentTranscriptWidth()
	height := model.currentTranscriptHeight()
	model.syncTranscriptCaches()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.renderTranscript(width, height)
	}
}

func BenchmarkRenderComposer(b *testing.B) {
	model := benchmarkRenderModel()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.renderComposer(model.width)
	}
}

func BenchmarkRenderStatusLine(b *testing.B) {
	model := benchmarkRenderModel()
	width := model.contentWidthFor(model.width, model.styles.status)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.renderStatusLine(width)
	}
}

func BenchmarkRenderFooter(b *testing.B) {
	model := benchmarkRenderModel()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.renderFooter(model.width)
	}
}

func BenchmarkCurrentTranscriptHeight(b *testing.B) {
	model := benchmarkRenderModel()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.currentTranscriptHeight()
	}
}

func BenchmarkRenderMainView(b *testing.B) {
	model := benchmarkRenderModel()
	model.syncTranscriptCaches()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.renderMainView()
	}
}

func BenchmarkRenderScreenOnly(b *testing.B) {
	model := benchmarkRenderModel()
	body := model.renderMainView()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = model.renderScreen(body)
	}
}

func BenchmarkShellKeypressCycle(b *testing.B) {
	model := benchmarkRenderModel()
	model.input = "git"
	model.cursor = len([]rune(model.input))
	model.syncTranscriptCaches()
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		next, _ := model.Update(key)
		model = next.(Model)
		_ = model.View()
		if utf8.RuneCountInString(model.input) > 12 {
			model.setInput("git")
			model.syncTranscriptCaches()
		}
	}
}

func BenchmarkAgentKeypressCycle(b *testing.B) {
	model := benchmarkRenderModel()
	model.mode = AgentMode
	model.input = "explain"
	model.cursor = len([]rune(model.input))
	model.syncTranscriptCaches()
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		next, _ := model.Update(key)
		model = next.(Model)
		_ = model.View()
		if utf8.RuneCountInString(model.input) > 16 {
			model.setInput("explain")
			model.syncTranscriptCaches()
		}
	}
}

func BenchmarkRecomputeShellCompletionWarm(b *testing.B) {
	model := benchmarkRenderModel()
	model.input = "git"
	model.cursor = len([]rune(model.input))
	model.recomputeCompletion()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		model.recomputeCompletion()
	}
}

func BenchmarkRecomputeShellCompletionCold(b *testing.B) {
	model := benchmarkRenderModel()
	model.input = "git"
	model.cursor = len([]rune(model.input))
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		model.shellCompletionCache = shellCompletionCache{}
		model.recomputeCompletion()
	}
}
