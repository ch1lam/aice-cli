package skill

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	maxNameLen        = 64
	maxDescriptionLen = 1024
	maxSkillBytes     = 256 << 10
)

var (
	errNoFrontmatter      = errors.New("missing yaml frontmatter")
	errInvalidUTF8        = errors.New("skill.md is not valid utf-8")
	errTooLarge           = errors.New("skill.md exceeds 256 kib")
	errMissingName        = errors.New("missing name")
	errMissingDescription = errors.New("missing description")
	errInvalidYAML        = errors.New("invalid yaml frontmatter")

	namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

	utf8BOM = []byte{0xEF, 0xBB, 0xBF}
)

type parsedSkill struct {
	Name        string
	Description string
	Body        string
	Warnings    []string
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func parseSkillMD(data []byte, dirName string) (parsedSkill, error) {
	if !utf8.Valid(data) {
		return parsedSkill{}, errInvalidUTF8
	}
	if len(data) > maxSkillBytes {
		return parsedSkill{}, errTooLarge
	}

	front, body, err := splitFrontmatter(data)
	if err != nil {
		return parsedSkill{}, err
	}

	var fm skillFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return parsedSkill{}, fmt.Errorf("%w: %w", errInvalidYAML, err)
	}

	name := strings.TrimSpace(fm.Name)
	description := strings.TrimSpace(fm.Description)
	if name == "" {
		return parsedSkill{}, errMissingName
	}
	if description == "" {
		return parsedSkill{}, errMissingDescription
	}

	var warnings []string
	if name != dirName {
		warnings = append(warnings, fmt.Sprintf(
			"name %q does not match parent directory %q",
			name,
			dirName,
		))
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		warnings = append(warnings, fmt.Sprintf(
			"name %q exceeds %d characters",
			name,
			maxNameLen,
		))
	}
	if !namePattern.MatchString(name) {
		warnings = append(warnings, fmt.Sprintf(
			"name %q is not lowercase alphanumeric with single hyphens",
			name,
		))
	}
	if utf8.RuneCountInString(description) > maxDescriptionLen {
		warnings = append(warnings, fmt.Sprintf(
			"description exceeds %d characters",
			maxDescriptionLen,
		))
	}

	return parsedSkill{
		Name:        name,
		Description: description,
		Body:        body,
		Warnings:    warnings,
	}, nil
}

func splitFrontmatter(data []byte) (front []byte, body string, err error) {
	data = bytes.TrimPrefix(data, utf8BOM)
	line, rest, found := cutLine(data)
	if strings.TrimSpace(string(line)) != "---" {
		return nil, "", errNoFrontmatter
	}
	if !found {
		return nil, "", errNoFrontmatter
	}

	var yamlLines []string
	remaining := rest
	for {
		line, next, found := cutLine(remaining)
		if strings.TrimSpace(string(line)) == "---" {
			return []byte(strings.Join(yamlLines, "\n")), string(next), nil
		}
		yamlLines = append(yamlLines, string(line))
		if !found {
			return nil, "", errNoFrontmatter
		}
		remaining = next
	}
}

func cutLine(data []byte) (line, rest []byte, ok bool) {
	i := bytes.IndexByte(data, '\n')
	if i < 0 {
		return data, nil, false
	}
	line = data[:i]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, data[i+1:], true
}
