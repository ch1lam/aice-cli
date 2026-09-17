package session_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func BenchmarkSessionHistory(b *testing.B) {
	for _, count := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "history.jsonl")
			file, err := os.Create(path)
			if err != nil {
				b.Fatal(err)
			}
			encoder := json.NewEncoder(file)
			if err := encoder.Encode(session.Header{Type: session.RecordTypeSession, Version: session.CurrentVersion, ID: "benchmark", CreatedAt: 1, WorkingDirectory: filepath.Dir(path)}); err != nil {
				b.Fatal(err)
			}
			parent := ""
			for i := range count {
				user := llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent(strings.Repeat("x", 1024)).Part()}, Timestamp: 1}
				entry, err := session.NewMessage(fmt.Sprint(i), parent, 1, user)
				if err != nil {
					b.Fatal(err)
				}
				if err := encoder.Encode(entry); err != nil {
					b.Fatal(err)
				}
				parent = entry.ID
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
			store, err := session.Open(b.Context(), path)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = store.Close() })
			b.Run("snapshot-context", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					snapshot, err := store.Snapshot()
					if err != nil {
						b.Fatal(err)
					}
					if _, err := session.BuildContext(snapshot); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("reopen", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					opened, err := session.Open(b.Context(), path)
					if err != nil {
						b.Fatal(err)
					}
					if err := opened.Close(); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
