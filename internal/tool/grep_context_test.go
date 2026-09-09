package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepContextCacheReusesReadsOnlyWithinCall(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file.txt")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("before\r\nneedle\r\nafter")
	cache := newGrepContextCache(maxGrepContextCacheBytes)
	lines, err := cache.read(path)
	if err != nil || strings.Join(lines, "|") != "before|needle|after" {
		t.Fatalf("lines=%q err=%v", lines, err)
	}
	write("changed")
	lines, err = cache.read(path)
	if err != nil || len(lines) != 3 {
		t.Fatalf("cached lines=%q err=%v", lines, err)
	}
	lines, err = newGrepContextCache(maxGrepContextCacheBytes).read(path)
	if err != nil || strings.Join(lines, "|") != "changed" {
		t.Fatalf("fresh lines=%q err=%v", lines, err)
	}
}

func TestGrepContextCacheRemembersReadFailure(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing.txt")
	cache := newGrepContextCache(maxGrepContextCacheBytes)
	if _, err := cache.read(path); err == nil {
		t.Fatal("expected missing file")
	}
	if err := os.WriteFile(path, []byte("created"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.read(path); err == nil {
		t.Fatal("failed read was retried")
	}
	if _, err := newGrepContextCache(maxGrepContextCacheBytes).read(path); err != nil {
		t.Fatal(err)
	}
}

func TestGrepContextCacheBudgetDoesNotSuppressContext(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lines.txt")
	// Tiny text with many lines: descriptors, not just source bytes, must count.
	content := strings.Repeat("\n", 100)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cache := newGrepContextCache(len(path) + len(content) + 1)
	lines, err := cache.read(path)
	if err != nil || len(lines) != 101 {
		t.Fatalf("context lost: %d lines, %v", len(lines), err)
	}
	if len(cache.entries) != 0 {
		t.Fatal("cached beyond descriptor budget")
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	lines, err = cache.read(path)
	if err != nil || strings.Join(lines, "|") != "changed" {
		t.Fatalf("uncached lines=%q err=%v", lines, err)
	}
}

func TestGrepContextCacheBoundsFailedEntries(t *testing.T) {
	t.Parallel()
	cache := newGrepContextCache(maxGrepContextCacheBytes)
	root := t.TempDir()
	for i := 0; i < maxGrepContextCacheFiles+10; i++ {
		if _, err := cache.read(filepath.Join(root, fmt.Sprint(i))); err == nil {
			t.Fatal("expected missing file")
		}
	}
	if len(cache.entries) != maxGrepContextCacheFiles || cache.remaining < 0 {
		t.Fatalf("cache exceeded budget: %d entries, %d remaining", len(cache.entries), cache.remaining)
	}
}
