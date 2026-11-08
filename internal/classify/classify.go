// Package classify decides whether a pending agent action may run in auto
// mode without asking the user.
//
// The classifier is reasoning-blind: it sees the user's own words, the
// agent's earlier tool calls and the pending call, never tool output or the
// agent's prose. Agent-authored content is JSON-encoded inside blocks framed
// by a per-classifier nonce. A fast first stage allows clear cases; anything
// else gets a second, reasoned stage over the same prompt prefix.
//
// The verdict is advisory. Deterministic gate floors (credentials, AICE
// configuration, privilege, disk) never reach the classifier.
package classify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// PromptVersion identifies the fixed template. Change it with every template
// edit and attach a fresh evaluation result.
const PromptVersion = "2026-11.3"

// Decision is the classifier outcome.
type Decision string

const (
	// Allow runs the action without asking.
	Allow Decision = "allow"
	// Block returns the reason to the agent so it can take a safer path.
	Block Decision = "block"
	// Unavailable means no judgement was made; a person decides.
	Unavailable Decision = "unavailable"
)

// Request is everything the classifier may see.
type Request struct {
	Intent      []UserTurn
	History     []PastAction
	Action      Action
	Environment Environment
}

// UserTurn is text the user typed, before attachments were inlined.
type UserTurn struct {
	Text string `json:"text"`
	Kind string `json:"kind,omitempty"` // prompt, steer or follow-up
}

// PastAction is an earlier tool call on the active branch, without output.
type PastAction struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Outcome   string          `json:"outcome,omitempty"` // executed, blocked, denied or failed
}

// Action is the pending tool call and the effects the gate resolved for it.
type Action struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Effects   []Effect        `json:"effects,omitempty"`
	// Reasons are the gate rules that would ask in default mode.
	Reasons []string `json:"reasons,omitempty"`
}

// Effect is one resolved consequence, such as a physical write target.
type Effect struct {
	Kind   string `json:"kind"`             // read, write, command, network or mcp
	Target string `json:"target,omitempty"` // path, host, command or service
	Rule   string `json:"rule,omitempty"`   // gate rule it triggered
}

// Environment describes the trust boundary for this Session.
type Environment struct {
	Workspace string   `json:"workspace,omitempty"`
	GitBranch string   `json:"git_branch,omitempty"`
	Remotes   []string `json:"remotes,omitempty"`
	Slots     Slots    `json:"-"`
}

// Slots extend the built-in template; they never replace it.
type Slots struct {
	Environment []string
	Block       []string
	Allow       []string
}

// Verdict is the classifier result for one action.
type Verdict struct {
	Decision Decision
	// Reason explains a block to the agent and the user, or why no
	// judgement was made.
	Reason string
	// Rule is the block rule ID the classifier cited, if any.
	Rule  string
	Stage int
	// Classifier is "<model>@<prompt version>".
	Classifier string
	Usage      llm.Usage
	Latency    time.Duration
}

// Model completes one prompt without tools. The application implements it.
type Model interface {
	Complete(ctx context.Context, req Completion) (string, llm.Usage, error)
}

// Completion is one model request.
type Completion struct {
	System    string
	Prompt    string
	MaxTokens int
	Thinking  llm.ThinkingLevel
}

// Config configures a Classifier.
type Config struct {
	Model Model
	// ModelName labels verdicts; it does not select a model.
	ModelName string
	// Zero timeouts use 8s for the first stage and 30s for the second.
	FastTimeout   time.Duration
	ReasonTimeout time.Duration
}

// Classifier is safe for concurrent use.
type Classifier struct {
	model         Model
	label         string
	nonce         string
	fastTimeout   time.Duration
	reasonTimeout time.Duration
}

// New returns a Classifier with a fresh framing nonce.
func New(cfg Config) (*Classifier, error) {
	if cfg.Model == nil {
		return nil, errors.New("classify: model is required")
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("classify: generate nonce: %w", err)
	}
	c := &Classifier{
		model:         cfg.Model,
		label:         strings.TrimSpace(cfg.ModelName) + "@" + PromptVersion,
		nonce:         hex.EncodeToString(nonce),
		fastTimeout:   cfg.FastTimeout,
		reasonTimeout: cfg.ReasonTimeout,
	}
	if c.fastTimeout <= 0 {
		c.fastTimeout = 8 * time.Second
	}
	if c.reasonTimeout <= 0 {
		c.reasonTimeout = 30 * time.Second
	}
	return c, nil
}

// Stage parameters. The first stage is a single word; reasoning-only models
// that cannot answer within the budget fall through to the second stage.
// Reasoning tokens count against the second stage's budget on most gateways.
const (
	fastMaxTokens   = 16
	reasonMaxTokens = 4096
)

// Classify judges one action. It returns an error only when ctx ends; every
// other failure is an Unavailable or Block verdict.
func (c *Classifier) Classify(ctx context.Context, req Request) (Verdict, error) {
	started := time.Now()
	system, prefix := c.prompt(req)
	verdict := Verdict{Classifier: c.label}
	finish := func() (Verdict, error) {
		verdict.Latency = time.Since(started)
		return verdict, ctx.Err()
	}

	text, usage, err := c.complete(ctx, c.fastTimeout, Completion{
		System: system, Prompt: prefix + fastInstruction, MaxTokens: fastMaxTokens, Thinking: llm.ThinkingLevelOff,
	})
	addUsage(&verdict.Usage, usage)
	if ctx.Err() != nil {
		return finish()
	}
	if err == nil && parseFast(text) {
		verdict.Decision, verdict.Stage = Allow, 1
		return finish()
	}

	verdict.Stage = 2
	text, usage, err = c.complete(ctx, c.reasonTimeout, Completion{
		System: system, Prompt: prefix + reasonInstruction, MaxTokens: reasonMaxTokens, Thinking: llm.ThinkingLevelLow,
	})
	addUsage(&verdict.Usage, usage)
	if ctx.Err() != nil {
		return finish()
	}
	if err != nil {
		verdict.Decision, verdict.Reason = Unavailable, "classifier unavailable: "+err.Error()
		return finish()
	}
	verdict.Decision, verdict.Rule, verdict.Reason = parseReasoned(text)
	return finish()
}

func (c *Classifier) complete(ctx context.Context, timeout time.Duration, req Completion) (string, llm.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.model.Complete(ctx, req)
}

func addUsage(total *llm.Usage, u llm.Usage) {
	total.InputTokens += u.InputTokens
	total.OutputTokens += u.OutputTokens
	total.ReasoningTokens += u.ReasoningTokens
	total.CacheReadTokens += u.CacheReadTokens
	total.CacheWriteTokens += u.CacheWriteTokens
	total.TotalTokens += u.TotalTokens
}
