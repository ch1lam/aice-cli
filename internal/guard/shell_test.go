package guard

import "testing"

func TestParsePathCandidates_AssignmentOnlyDoesNotPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
	}{
		{name: "single assignment", command: "VAR=val"},
		{name: "multiple assignments", command: "A=1 B=2"},
		{name: "assignment with command", command: "FOO=bar ls -la"},
		{name: "empty", command: ""},
		{name: "blank", command: "   "},
		{name: "semicolon only", command: ";"},
		{name: "pipe only", command: "|"},
		{name: "redirect only", command: "> file"},
		{name: "ls sessions", command: "ls -lt .aice/sessions/ | head -n 20"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_ = parsePathCandidates(tt.command)
			_ = parseCallWords(tt.command)
		})
	}
}
