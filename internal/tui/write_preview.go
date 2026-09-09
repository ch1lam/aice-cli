package tui

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
)

const maximumWritePreviewBytes = 64 * 1024

// This state belongs only to the update loop. No recovered JSON is executable.
type writePreview struct {
	raw           strings.Builder
	content       string
	path          string
	known         bool
	truncated     bool
	revision      uint64
	cacheRevision uint64
	cacheWidth    int
	cacheExpanded bool
	rendered      string
	codeSource    string
	codePath      string
	codeWidth     int
	codeRendered  string
}

func (m *model) applyWriteDelta(delta DisplayDelta) bool {
	index := -1
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := &m.entries[i]
		if e.kind == entryTool && e.toolAssistant == m.assistantEntry && e.toolIndex == delta.ToolIndex && e.toolPreparing {
			index = i
			break
		}
	}
	if index < 0 {
		if delta.Tool.Name != "write" {
			return false
		}
		m.entries = append(m.entries, transcriptEntry{
			kind:          entryTool,
			processID:     m.ensureActiveProcess(),
			toolName:      "write",
			toolID:        delta.Tool.ID,
			toolAssistant: m.assistantEntry,
			toolIndex:     delta.ToolIndex,
			toolPreparing: true,
			writePreview:  &writePreview{},
		})
		index = len(m.entries) - 1
	}
	e := &m.entries[index]
	if delta.Tool.ID != "" {
		e.toolID = delta.Tool.ID
	}
	p := e.writePreview
	if delta.ToolEnd {
		p.setContent(delta.Tool)
		e.toolDetail = sanitizeToolDetail(delta.Tool.Detail, false)
	} else {
		remaining := maximumWritePreviewBytes - p.raw.Len()
		if remaining == 0 {
			if !p.truncated && delta.Arguments != "" {
				p.truncated = true
				p.revision++
				e.toolPreviewRevision = p.revision
				return true
			}
			return false
		}
		p.raw.WriteString(delta.Arguments[:min(len(delta.Arguments), remaining)])
		p.truncated = len(delta.Arguments) > remaining
		p.revision++
	}
	e.toolPreviewRevision = p.revision
	return true
}

func (m *model) startDisplayedTool(tool ToolDisplay) {
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := &m.entries[i]
		if e.kind == entryTool && e.toolPreparing && e.toolID == tool.ID {
			e.toolPreparing = false
			e.toolDetail = sanitizeToolDetail(tool.Detail, false)
			e.writePreview.setContent(tool)
			e.toolPreviewRevision = e.writePreview.revision
			return
		}
	}
	e := transcriptEntry{
		kind:       entryTool,
		processID:  m.ensureActiveProcess(),
		toolID:     tool.ID,
		toolName:   tool.Name,
		toolDetail: sanitizeToolDetail(tool.Detail, tool.Name == "bash"),
	}
	if tool.Name == "write" {
		e.writePreview = &writePreview{}
		e.writePreview.setContent(tool)
		e.toolPreviewRevision = e.writePreview.revision
	}
	m.entries = append(m.entries, e)
}

func (p *writePreview) setContent(tool ToolDisplay) {
	p.raw.Reset()
	p.path = tool.Detail
	p.content = strings.Clone(writePrefix(tool.Content, maximumWritePreviewBytes))
	p.known = tool.HasContent
	p.truncated = len(p.content) < len(tool.Content)
	p.revision++
}

func writePrefix(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	end := limit
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// Decode only the flat string fields already received. A partial string is
// closed at its last complete JSON escape; malformed values stay undisplayed.
func partialWriteFields(raw string) (path, content string, known bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return path, content, known
		}
		offset := int(decoder.InputOffset())
		var value string
		if err := decoder.Decode(&value); err != nil {
			if key == "content" {
				rest := strings.TrimLeft(raw[offset:], " \t\r\n:")
				value, known = partialJSONString(rest)
				content = value
			}
			return path, content, known
		}
		switch key {
		case "path":
			path = value
		case "content":
			content, known = value, true
		}
	}
	return
}

func partialJSONString(raw string) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	end := 1
	for i := 1; i < len(raw); {
		if raw[i] == '"' {
			end = i
			break
		}
		next := i + 1
		if raw[i] == '\\' {
			if next >= len(raw) {
				break
			}
			next++
			if raw[i+1] == 'u' {
				next += 4
			}
			if next > len(raw) {
				break
			}
			if raw[i+1] == 'u' {
				code, err := strconv.ParseUint(raw[i+2:next], 16, 16)
				if err != nil {
					break
				}
				if code >= 0xd800 && code <= 0xdbff {
					if next+6 > len(raw) || raw[next:next+2] != `\u` {
						break
					}
					low, err := strconv.ParseUint(raw[next+2:next+6], 16, 16)
					if err != nil || low < 0xdc00 || low > 0xdfff {
						break
					}
					next += 6
				}
			}
		} else if raw[i] >= utf8.RuneSelf {
			if !utf8.FullRuneInString(raw[i:]) {
				break
			}
			_, size := utf8.DecodeRuneInString(raw[i:])
			next = i + size
		}
		end, i = next, next
	}
	var value string
	err := json.Unmarshal([]byte(raw[:end]+"\""), &value)
	return value, err == nil
}

func (p *writePreview) view(width int, expanded bool) string {
	if p.cacheWidth == width && p.cacheExpanded == expanded && p.cacheRevision == p.revision {
		return p.rendered
	}
	content, path, known := p.content, p.path, p.known
	if !known {
		path, content, known = partialWriteFields(p.raw.String())
	}
	result := mutedStyle.Render("Waiting for content…")
	if known {
		limit, lines := 4096, 10
		if expanded {
			limit, lines = maximumWritePreviewBytes, 2000
		}
		source := writePrefix(content, limit)
		omitted := len(source) < len(content) || p.truncated
		rows := strings.SplitN(source, "\n", lines+1)
		if len(rows) > lines {
			rows = rows[:lines]
			omitted = true
		}
		for i, row := range rows {
			row = sanitizeToolDetail(strings.TrimSuffix(row, "\r"), true)
			row = strings.ReplaceAll(row, "\t", "    ")
			rows[i] = ansi.Truncate(row, max(width-4, 1), "…")
		}
		source = strings.Join(rows, "\n")
		result = mutedStyle.Render("(empty file)")
		if content != "" {
			result = p.codeView(source, path, width)
		}
		if omitted {
			notice := "… preview limited · ctrl+o collapse/expand for more"
			if expanded {
				notice = "… preview limit reached (64 KiB / 2000 lines)"
			}
			result += "\n" + mutedStyle.Render(notice)
		}
	}
	p.cacheWidth, p.cacheExpanded, p.cacheRevision, p.rendered = width, expanded, p.revision, result
	return result
}

// Reuse highlighted rows once the visible prefix stops changing, even while
// later argument bytes continue arriving in the same bounded stream.
func (p *writePreview) codeView(source, path string, width int) string {
	if p.codeSource == source && p.codePath == path && p.codeWidth == width {
		return p.codeRendered
	}
	// Use the existing theme/highlighter, with a fence that content cannot close.
	fence := "```"
	for strings.Contains(source, fence) {
		fence += "`"
	}
	language := strings.TrimPrefix(filepath.Ext(path), ".")
	for _, r := range language {
		if r < 'a' || r > 'z' {
			language = ""
			break
		}
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(inkMarkdownStyle()), glamour.WithWordWrap(width))
	result := source
	if err == nil {
		if rendered, err := renderer.Render(fence + language + "\n" + source + "\n" + fence); err == nil {
			result = strings.Trim(rendered, "\r\n")
		}
	}
	p.codeSource, p.codePath, p.codeWidth, p.codeRendered = source, path, width, result
	return result
}
