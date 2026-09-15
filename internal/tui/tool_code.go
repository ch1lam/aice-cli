package tui

import (
	"path"
	"strings"
)

func toolCodeLanguage(filename string) string {
	filename = path.Base(strings.ReplaceAll(filename, `\`, "/"))
	switch strings.ToLower(filename) {
	case "dockerfile", "makefile":
		return strings.ToLower(filename)
	}
	language := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
	if language == "" {
		return "text"
	}
	for _, r := range language {
		if r < 'a' || r > 'z' {
			return "text"
		}
	}
	return language
}
