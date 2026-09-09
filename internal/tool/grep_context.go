package tool

import "strconv"

const (
	maxGrepContextCacheBytes = 16 * 1024 * 1024
	maxGrepContextCacheFiles = 128
)

type grepContextEntry struct {
	lines []string
	err   error
}

// The cache belongs to one formatting pass. Admission limits retained file data
// and line descriptors; the entry cap also bounds map and failed-read overhead.
// Files that do not fit are still readable, but are not retained.
type grepContextCache struct {
	entries   map[string]grepContextEntry
	remaining int
}

func newGrepContextCache(budget int) *grepContextCache {
	return &grepContextCache{entries: make(map[string]grepContextEntry), remaining: budget}
}

func (c *grepContextCache) read(path string) ([]string, error) {
	if entry, ok := c.entries[path]; ok {
		return entry.lines, entry.err
	}
	lines, err := readGrepLines(path)
	// Split strings share one backing text allocation, including its newlines.
	// Each string descriptor contains a pointer and a length (two machine words).
	cost := len(path) + len(lines)*(2*strconv.IntSize/8)
	if len(lines) > 0 {
		cost += len(lines) - 1
	}
	for _, line := range lines {
		cost += len(line)
	}
	if err != nil {
		cost += len(err.Error())
	}
	if cost <= c.remaining && len(c.entries) < maxGrepContextCacheFiles {
		c.entries[path] = grepContextEntry{lines: lines, err: err}
		c.remaining -= cost
	}
	return lines, err
}
