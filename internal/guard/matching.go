package guard

import (
	"path"
	"regexp"
	"strings"
)

// PatternConfig mirrors pi-guardrails PatternConfig.
type PatternConfig struct {
	Pattern     string `json:"pattern"`
	Description string `json:"description,omitempty"`
	Regex       bool   `json:"regex,omitempty"`
}

// compiledPattern is the runtime form of a PatternConfig.
type compiledPattern struct {
	test   func(string) bool
	source PatternConfig
}

// normalizeFilePath normalizes a path for matching: forward slashes, strip
// leading "./", collapse "//".
func normalizeFilePath(input string) string {
	normalized := strings.ReplaceAll(input, "\\", "/")
	for strings.HasPrefix(normalized, "./") {
		normalized = normalized[2:]
	}
	for strings.Contains(normalized, "//") {
		normalized = strings.ReplaceAll(normalized, "//", "/")
	}
	return normalized
}

func compileFilePattern(cfg PatternConfig) compiledPattern {
	if cfg.Regex {
		re, err := regexp.Compile("(?i)" + cfg.Pattern)
		if err != nil {
			return compiledPattern{test: func(string) bool { return false }, source: cfg}
		}
		return compiledPattern{
			test:   func(s string) bool { return re.MatchString(normalizeFilePath(s)) },
			source: cfg,
		}
	}
	// Handle "**" prefix / directory globs without relying on filepath.Match which
	// does not support **.  Treat trailing "/**" as prefix match.
	if strings.HasSuffix(cfg.Pattern, "/**") {
		prefix := normalizeFilePath(strings.TrimSuffix(cfg.Pattern, "/**"))
		return compiledPattern{
			test: func(s string) bool {
				n := normalizeFilePath(s)
				return n == prefix || strings.HasPrefix(n, prefix+"/")
			},
			source: cfg,
		}
	}
	// Generic "**" fallback: collapse to "*"
	pat := normalizeFilePath(cfg.Pattern)
	if strings.Contains(pat, "**") {
		pat = strings.ReplaceAll(pat, "**", "*")
	}
	matchFullPath := strings.Contains(pat, "/")
	return compiledPattern{
		test: func(s string) bool {
			n := normalizeFilePath(s)
			candidate := n
			if !matchFullPath {
				if idx := strings.LastIndex(n, "/"); idx >= 0 {
					candidate = n[idx+1:]
				}
			}
			matched, _ := path.Match(pat, candidate)
			return matched
		},
		source: cfg,
	}
}

func compileFilePatterns(cfgs []PatternConfig) []compiledPattern {
	out := make([]compiledPattern, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, compileFilePattern(c))
	}
	return out
}
