//go:build integration

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// TestClassifierEval scores the auto mode classifier on the labeled cases in
// internal/classify/eval with the configured provider and model. It makes
// paid requests, so it needs the integration tag and an explicit opt-in:
//
//	AICE_CLASSIFY_EVAL=1 AICE_PROVIDER=opencode-go AICE_MODEL=deepseek-v4.1-flash \
//	  go test -tags=integration ./internal/app -run TestClassifierEval -v -timeout 30m
//
// AICE_CLASSIFY_EVAL_FILTER limits cases by ID substring and
// AICE_CLASSIFY_EVAL_CONCURRENCY sets parallel requests (default 4). The
// report is written to internal/classify/eval/results/<model>@<version>.json.
func TestClassifierEval(t *testing.T) {
	if os.Getenv("AICE_CLASSIFY_EVAL") != "1" {
		t.Skip("set AICE_CLASSIFY_EVAL=1 to run the paid classifier evaluation")
	}
	cases, err := classify.LoadCases(os.DirFS(filepath.Join("..", "classify", "eval")))
	if err != nil {
		t.Fatal(err)
	}
	if filter := os.Getenv("AICE_CLASSIFY_EVAL_FILTER"); filter != "" {
		var kept []classify.Case
		for _, c := range cases {
			if strings.Contains(c.ID, filter) {
				kept = append(kept, c)
			}
		}
		cases = kept
	}
	providers := defaultProviders()
	a := &application{dependencies: dependencies{
		loadConfig: config.Load,
		providers:  providers,
		newModel: func(c config.Config) (llm.Streamer, error) {
			return modelForConfiguration(providers, c)
		},
	}}
	configured, err := a.loadConfiguredModel(config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.initializeConfiguredModel(&configured); err != nil {
		t.Fatal(err)
	}
	classifier, err := classify.New(classify.Config{Model: classifierModel{configured: configured}, ModelName: configured.model.ID})
	if err != nil {
		t.Fatal(err)
	}

	workers := 4
	if n, err := strconv.Atoi(os.Getenv("AICE_CLASSIFY_EVAL_CONCURRENCY")); err == nil && n > 0 {
		workers = n
	}
	verdicts := make([]classify.Verdict, len(cases))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				v, err := classifier.Classify(t.Context(), cases[i].Request())
				if err != nil {
					v = classify.Verdict{Decision: classify.Unavailable, Reason: err.Error()}
				}
				verdicts[i] = v
			}
		})
	}
	for i := range cases {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	report := classify.Score(cases, verdicts)
	for _, set := range []string{classify.SetBenign, classify.SetOvereager, classify.SetInjection} {
		if s := report.Sets[set]; s != nil {
			t.Logf("%-9s total=%d allowed=%d blocked=%d unavailable=%d error_rate=%.1f%% (threshold %.1f%%)",
				set, s.Total, s.Allowed, s.Blocked, s.Unavailable, s.Rate*100, s.Threshold*100)
		}
	}
	t.Logf("fast allows=%d (mean %dms) reasoned=%d mean latency=%dms tokens=%d", report.FastAllows, report.FastMillis, report.Reasoned, report.MeanMillis, report.Tokens)
	for _, miss := range report.Misses {
		t.Logf("miss %-9s %-28s got=%s %s", miss.Set, miss.ID, miss.Got, miss.Reason)
	}
	for i, v := range verdicts {
		if v.Decision == classify.Unavailable {
			t.Logf("unavailable %-28s %s", cases[i].ID, v.Reason)
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("..", "classify", "eval", "results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := strings.NewReplacer("/", "_", " ", "_").Replace(report.Classifier) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, failure := range report.Failures() {
		t.Error(failure)
	}
}
