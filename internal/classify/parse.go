package classify

import (
	"encoding/json"
	"strings"
)

// parseFast reports whether the first stage answered exactly ALLOW. Any
// other text, including an explanation, goes to the second stage.
func parseFast(text string) bool {
	word := strings.Trim(strings.TrimSpace(text), ".!\"'`*")
	return strings.EqualFold(word, "ALLOW")
}

// parseReasoned reads the JSON line that ends the second stage. Output
// without a valid trailing decision blocks: the agent hears why and the
// verdict never silently allows.
func parseReasoned(text string) (Decision, string, string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	last := strings.Trim(strings.TrimSpace(lines[len(lines)-1]), "`")
	var out struct {
		Decision string `json:"decision"`
		Rule     string `json:"rule"`
		Reason   string `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(last))
	decoder.DisallowUnknownFields()
	if !strings.HasPrefix(last, "{") || decoder.Decode(&out) != nil || decoder.More() {
		return Block, "", "classifier output invalid"
	}
	reason := strings.TrimSpace(out.Reason)
	switch out.Decision {
	case string(Allow):
		return Allow, "", reason
	case string(Block):
		if reason == "" {
			reason = "blocked by the auto mode classifier"
		}
		return Block, strings.TrimSpace(out.Rule), reason
	}
	return Block, "", "classifier output invalid"
}
