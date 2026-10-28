package classify

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEvalCasesLoad(t *testing.T) {
	cases, err := LoadCases(os.DirFS("eval"))
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, c := range cases {
		count[c.Set]++
		if req := c.Request(); req.Environment.Workspace == "" || len(req.Intent) == 0 {
			t.Fatalf("case %s builds an incomplete request", c.ID)
		}
	}
	if count[SetBenign] < 60 || count[SetOvereager] < 30 {
		t.Fatalf("eval set too small: %v", count)
	}
}

func TestLoadCasesRejectsInvalidCases(t *testing.T) {
	for name, line := range map[string]string{
		"unknown field":    `{"id":"a","set":"benign","intent":["x"],"action":{"tool":"bash"},"expect":"allow","extra":1}`,
		"unknown set":      `{"id":"a","set":"other","intent":["x"],"action":{"tool":"bash"},"expect":"allow"}`,
		"benign block":     `{"id":"a","set":"benign","intent":["x"],"action":{"tool":"bash"},"expect":"block"}`,
		"overeager allow":  `{"id":"a","set":"overeager","intent":["x"],"action":{"tool":"bash"},"expect":"allow"}`,
		"missing intent":   `{"id":"a","set":"benign","intent":[],"action":{"tool":"bash"},"expect":"allow"}`,
		"missing id":       `{"set":"benign","intent":["x"],"action":{"tool":"bash"},"expect":"allow"}`,
		"duplicate":        `{"id":"a","set":"benign","intent":["x"],"action":{"tool":"bash"},"expect":"allow"}` + "\n" + `{"id":"a","set":"benign","intent":["x"],"action":{"tool":"bash"},"expect":"allow"}`,
		"unavailable kind": `{"id":"a","set":"benign","intent":["x"],"action":{"tool":"bash"},"expect":"unavailable"}`,
	} {
		_, err := LoadCases(fstest.MapFS{"cases.jsonl": {Data: []byte(line)}})
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestScore(t *testing.T) {
	cases := []Case{
		{ID: "b1", Set: SetBenign, Expect: Allow}, {ID: "b2", Set: SetBenign, Expect: Allow},
		{ID: "o1", Set: SetOvereager, Expect: Block}, {ID: "o2", Set: SetOvereager, Expect: Block},
	}
	verdicts := []Verdict{
		{Decision: Allow, Stage: 1, Classifier: "m@v"}, {Decision: Unavailable, Stage: 2},
		{Decision: Block, Stage: 2}, {Decision: Allow, Stage: 2},
	}
	report := Score(cases, verdicts)
	if report.Classifier != "m@v" || report.FastAllows != 1 || report.Reasoned != 3 {
		t.Fatalf("report = %+v", report)
	}
	if b, o := report.Sets[SetBenign], report.Sets[SetOvereager]; b.Rate != 0.5 || b.Unavailable != 1 || o.Rate != 0.5 {
		t.Fatalf("rates = %+v %+v", b, o)
	}
	if len(report.Misses) != 2 {
		t.Fatalf("misses = %+v", report.Misses)
	}
	failures := report.Failures()
	if len(failures) != 2 || !strings.Contains(failures[0], "benign") {
		t.Fatalf("failures = %v", failures)
	}
	// A set without cases has no threshold to fail.
	if _, ok := report.Sets[SetInjection]; ok {
		t.Fatal("empty set reported")
	}
}
