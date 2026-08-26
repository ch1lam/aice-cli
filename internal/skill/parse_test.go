package skill

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseSkillMD(t *testing.T) {
	t.Parallel()

	longName := strings.Repeat("a", maxNameLen+1)
	longDescription := strings.Repeat("d", maxDescriptionLen+1)

	tests := []struct {
		name         string
		dir          string
		input        string
		wantName     string
		wantDesc     string
		wantBody     string
		wantErr      error
		wantWarnings []string
	}{
		{
			name:     "valid minimal",
			dir:      "pdf-processing",
			input:    "---\nname: pdf-processing\ndescription: Extract PDFs. Use when handling PDFs.\n---\n\n# PDF\n",
			wantName: "pdf-processing",
			wantDesc: "Extract PDFs. Use when handling PDFs.",
			wantBody: "\n# PDF\n",
		},
		{
			name: "optional fields ignored",
			dir:  "pdf-processing",
			input: "---\nname: pdf-processing\ndescription: Extract PDFs. Use when handling PDFs.\n" +
				"license: MIT\ncompatibility: needs pdftotext\nmetadata:\n  author: aice\n" +
				"allowed-tools: Read Bash\n---\nbody\n",
			wantName: "pdf-processing",
			wantDesc: "Extract PDFs. Use when handling PDFs.",
			wantBody: "body\n",
		},
		{
			name:     "crlf delimiters",
			dir:      "pdf-processing",
			input:    "---\r\nname: pdf-processing\r\ndescription: Extract PDFs. Use when handling PDFs.\r\n---\r\nbody",
			wantName: "pdf-processing",
			wantDesc: "Extract PDFs. Use when handling PDFs.",
			wantBody: "body",
		},
		{
			name:     "utf-8 bom then frontmatter",
			dir:      "pdf-processing",
			input:    "\uFEFF---\nname: pdf-processing\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName: "pdf-processing",
			wantDesc: "Extract PDFs. Use when handling PDFs.",
			wantBody: "",
		},
		{
			name:    "missing frontmatter",
			dir:     "pdf-processing",
			input:   "# just markdown\n",
			wantErr: errNoFrontmatter,
		},
		{
			name:    "missing closing delimiter",
			dir:     "pdf-processing",
			input:   "---\nname: pdf-processing\ndescription: Extract PDFs. Use when handling PDFs.\n",
			wantErr: errNoFrontmatter,
		},
		{
			name:    "invalid yaml",
			dir:     "pdf-processing",
			input:   "---\nname: [broken\n---\n",
			wantErr: errInvalidYAML,
		},
		{
			name:    "missing name",
			dir:     "pdf-processing",
			input:   "---\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantErr: errMissingName,
		},
		{
			name:    "empty name",
			dir:     "pdf-processing",
			input:   "---\nname: \"  \"\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantErr: errMissingName,
		},
		{
			name:    "missing description",
			dir:     "pdf-processing",
			input:   "---\nname: pdf-processing\n---\n",
			wantErr: errMissingDescription,
		},
		{
			name:    "empty description",
			dir:     "pdf-processing",
			input:   "---\nname: pdf-processing\ndescription: \"\"\n---\n",
			wantErr: errMissingDescription,
		},
		{
			name:         "name directory mismatch still loads",
			dir:          "other-dir",
			input:        "---\nname: pdf-processing\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName:     "pdf-processing",
			wantDesc:     "Extract PDFs. Use when handling PDFs.",
			wantWarnings: []string{`name "pdf-processing" does not match parent directory "other-dir"`},
		},
		{
			name:         "name too long still loads",
			dir:          longName,
			input:        "---\nname: " + longName + "\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName:     longName,
			wantDesc:     "Extract PDFs. Use when handling PDFs.",
			wantWarnings: []string{`name "` + longName + `" exceeds 64 characters`},
		},
		{
			name:         "uppercase name still loads",
			dir:          "PDF-Processing",
			input:        "---\nname: PDF-Processing\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName:     "PDF-Processing",
			wantDesc:     "Extract PDFs. Use when handling PDFs.",
			wantWarnings: []string{`name "PDF-Processing" is not lowercase alphanumeric with single hyphens`},
		},
		{
			name:         "leading hyphen still loads",
			dir:          "-pdf",
			input:        "---\nname: -pdf\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName:     "-pdf",
			wantDesc:     "Extract PDFs. Use when handling PDFs.",
			wantWarnings: []string{`name "-pdf" is not lowercase alphanumeric with single hyphens`},
		},
		{
			name:         "trailing hyphen still loads",
			dir:          "pdf-",
			input:        "---\nname: pdf-\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName:     "pdf-",
			wantDesc:     "Extract PDFs. Use when handling PDFs.",
			wantWarnings: []string{`name "pdf-" is not lowercase alphanumeric with single hyphens`},
		},
		{
			name:         "consecutive hyphens still loads",
			dir:          "pdf--processing",
			input:        "---\nname: pdf--processing\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName:     "pdf--processing",
			wantDesc:     "Extract PDFs. Use when handling PDFs.",
			wantWarnings: []string{`name "pdf--processing" is not lowercase alphanumeric with single hyphens`},
		},
		{
			name:         "description too long still loads",
			dir:          "pdf-processing",
			input:        "---\nname: pdf-processing\ndescription: " + longDescription + "\n---\n",
			wantName:     "pdf-processing",
			wantDesc:     longDescription,
			wantWarnings: []string{"description exceeds 1024 characters"},
		},
		{
			name:     "name at max length is valid",
			dir:      strings.Repeat("a", maxNameLen),
			input:    "---\nname: " + strings.Repeat("a", maxNameLen) + "\ndescription: Extract PDFs. Use when handling PDFs.\n---\n",
			wantName: strings.Repeat("a", maxNameLen),
			wantDesc: "Extract PDFs. Use when handling PDFs.",
		},
		{
			name:     "description at max length is valid",
			dir:      "pdf-processing",
			input:    "---\nname: pdf-processing\ndescription: " + strings.Repeat("d", maxDescriptionLen) + "\n---\n",
			wantName: "pdf-processing",
			wantDesc: strings.Repeat("d", maxDescriptionLen),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := parseSkillMD([]byte(tt.input), tt.dir)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("parseSkillMD() error = nil, want %v", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("parseSkillMD() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSkillMD() error = %v", err)
			}
			if parsed.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", parsed.Name, tt.wantName)
			}
			if parsed.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", parsed.Description, tt.wantDesc)
			}
			if parsed.Body != tt.wantBody {
				t.Errorf("Body = %q, want %q", parsed.Body, tt.wantBody)
			}
			if len(parsed.Warnings) != len(tt.wantWarnings) {
				t.Fatalf("Warnings = %#v, want %#v", parsed.Warnings, tt.wantWarnings)
			}
			for i, warning := range tt.wantWarnings {
				if parsed.Warnings[i] != warning {
					t.Errorf("Warnings[%d] = %q, want %q", i, parsed.Warnings[i], warning)
				}
			}
			if utf8.RuneCountInString(parsed.Name) == 0 {
				t.Fatal("loaded skill has empty name")
			}
		})
	}
}
