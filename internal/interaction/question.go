package interaction

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Question limits bound one request_user_input call. They keep panels
// compact and answers replay-safe; Execute rejects anything outside them
// before any UI is shown.
const (
	MaxQuestionsPerRequest    = 3
	MaxOptionsPerQuestion     = 3
	MaxQuestionHeaderRunes    = 80
	MaxQuestionTextRunes      = 2000
	MaxOptionLabelRunes       = 120
	MaxOptionDescriptionRunes = 300
	MaxAnswerTextRunes        = 2000
)

var questionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// QuestionOption is one selectable answer for a question.
type QuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionItem is one question inside a request_user_input call. Options may
// be empty for a free-text question. RecommendedOptionID is optional and
// must reference one of Options; it only sets initial focus, never an answer.
type QuestionItem struct {
	ID                  string           `json:"id"`
	Header              string           `json:"header,omitempty"`
	Question            string           `json:"question"`
	Options             []QuestionOption `json:"options,omitempty"`
	RecommendedOptionID string           `json:"recommended_option_id,omitempty"`
}

// QuestionRequest is the frontend-neutral payload for one tool call. ID is
// the originating tool-call ID assigned by the app, not model-generated text,
// so late or duplicate replies can never be attributed to another request.
type QuestionRequest struct {
	ID        string         `json:"-"`
	Questions []QuestionItem `json:"questions"`
}

// QuestionAnswerStatus distinguishes an explicit answer from an explicit skip.
// A skip is never interpreted as consent, a recommendation, or a delegation.
type QuestionAnswerStatus string

const (
	QuestionAnswered QuestionAnswerStatus = "answered"
	QuestionSkipped  QuestionAnswerStatus = "skipped"
)

// QuestionAnswer is one submitted answer. Option questions accept either a
// selected option (SelectedOptionID/SelectedLabel set, Text optional
// supplement) or a custom answer (both selected fields empty, Text non-empty).
// For free-text questions Text is the answer itself and the selected fields
// stay empty.
type QuestionAnswer struct {
	Status           QuestionAnswerStatus `json:"status"`
	SelectedOptionID string               `json:"selected_option_id,omitempty"`
	SelectedLabel    string               `json:"selected_label,omitempty"`
	Text             string               `json:"text,omitempty"`
}

// QuestionReply carries the whole submitted group. Answers must cover every
// requested question ID exactly once; per-question edits before submit never
// produce intermediate replies.
type QuestionReply struct {
	RequestID string                    `json:"-"`
	Answers   map[string]QuestionAnswer `json:"answers"`
}

// QuestionAsker is the small capability a question tool consumes. The app
// implements it; the Agent Loop never sees UI details.
type QuestionAsker interface {
	AskQuestion(ctx context.Context, request QuestionRequest) (QuestionReply, error)
}

// QuestionPrompt is the live exchange behind one AskQuestion call. Reply
// carries at most one submission; Done closes when the call is answered,
// cancelled, or superseded. Frontends keep only a display copy.
type QuestionPrompt struct {
	Request QuestionRequest
	Done    <-chan struct{}
	Reply   chan QuestionReply
}

// QuestionRequester exposes pending question prompts to a frontend.
type QuestionRequester interface {
	QuestionRequests() <-chan QuestionPrompt
}

// ValidateQuestionRequest checks one tool call before any UI is shown.
func ValidateQuestionRequest(request QuestionRequest) error {
	if strings.TrimSpace(request.ID) == "" {
		return fmt.Errorf("interaction: question request id is required")
	}
	if len(request.Questions) == 0 || len(request.Questions) > MaxQuestionsPerRequest {
		return fmt.Errorf(
			"interaction: questions must contain 1-%d items, got %d",
			MaxQuestionsPerRequest,
			len(request.Questions),
		)
	}
	seen := make(map[string]struct{}, len(request.Questions))
	for index := range request.Questions {
		item := &request.Questions[index]
		if !questionIDPattern.MatchString(item.ID) {
			return fmt.Errorf(
				"interaction: questions[%d].id must match [A-Za-z0-9_-]{1,64}, got %q",
				index,
				item.ID,
			)
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("interaction: duplicate question id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if utf8.RuneCountInString(item.Header) > MaxQuestionHeaderRunes {
			return fmt.Errorf(
				"interaction: questions[%q].header exceeds %d runes",
				item.ID,
				MaxQuestionHeaderRunes,
			)
		}
		if strings.TrimSpace(item.Question) == "" || utf8.RuneCountInString(item.Question) > MaxQuestionTextRunes {
			return fmt.Errorf(
				"interaction: questions[%q].question must contain 1-%d runes",
				item.ID,
				MaxQuestionTextRunes,
			)
		}
		if len(item.Options) > MaxOptionsPerQuestion {
			return fmt.Errorf(
				"interaction: questions[%q].options must contain at most %d items",
				item.ID,
				MaxOptionsPerQuestion,
			)
		}
		optionSeen := make(map[string]struct{}, len(item.Options))
		for optionIndex := range item.Options {
			option := &item.Options[optionIndex]
			if !questionIDPattern.MatchString(option.ID) {
				return fmt.Errorf(
					"interaction: questions[%q].options[%d].id must match [A-Za-z0-9_-]{1,64}, got %q",
					item.ID,
					optionIndex,
					option.ID,
				)
			}
			if _, exists := optionSeen[option.ID]; exists {
				return fmt.Errorf(
					"interaction: questions[%q] has duplicate option id %q",
					item.ID,
					option.ID,
				)
			}
			optionSeen[option.ID] = struct{}{}
			if strings.TrimSpace(option.Label) == "" || utf8.RuneCountInString(option.Label) > MaxOptionLabelRunes {
				return fmt.Errorf(
					"interaction: questions[%q].options[%q].label must contain 1-%d runes",
					item.ID,
					option.ID,
					MaxOptionLabelRunes,
				)
			}
			if utf8.RuneCountInString(option.Description) > MaxOptionDescriptionRunes {
				return fmt.Errorf(
					"interaction: questions[%q].options[%q].description exceeds %d runes",
					item.ID,
					option.ID,
					MaxOptionDescriptionRunes,
				)
			}
		}
		if item.RecommendedOptionID != "" {
			_, ok := optionSeen[item.RecommendedOptionID]
			if !ok {
				return fmt.Errorf(
					"interaction: questions[%q].recommended_option_id %q does not reference an option",
					item.ID,
					item.RecommendedOptionID,
				)
			}
		}
		if !utf8.ValidString(item.Header) ||
			!utf8.ValidString(item.Question) ||
			!utf8.ValidString(item.RecommendedOptionID) {
			return fmt.Errorf("interaction: questions[%q] contains invalid UTF-8", item.ID)
		}
		for _, option := range item.Options {
			if !utf8.ValidString(option.Label) || !utf8.ValidString(option.Description) {
				return fmt.Errorf("interaction: questions[%q] contains invalid UTF-8", item.ID)
			}
		}
	}
	return nil
}

// ValidateQuestionReply checks one submission against its validated request.
// It runs on the answered path so a faulty frontend can never inject a
// malformed result into the Session.
func ValidateQuestionReply(request QuestionRequest, reply QuestionReply) error {
	if reply.RequestID != request.ID {
		return fmt.Errorf("interaction: question reply targets %q, want %q", reply.RequestID, request.ID)
	}
	if len(reply.Answers) != len(request.Questions) {
		return fmt.Errorf(
			"interaction: question reply must answer all %d questions, got %d",
			len(request.Questions),
			len(reply.Answers),
		)
	}
	for index := range request.Questions {
		item := &request.Questions[index]
		answer, ok := reply.Answers[item.ID]
		if !ok {
			return fmt.Errorf("interaction: question reply is missing answer for %q", item.ID)
		}
		if err := validateAnswer(item, answer); err != nil {
			return err
		}
	}
	return nil
}

func validateAnswer(item *QuestionItem, answer QuestionAnswer) error {
	switch answer.Status {
	case QuestionAnswered:
	case QuestionSkipped:
		if answer.SelectedOptionID != "" || answer.SelectedLabel != "" || answer.Text != "" {
			return fmt.Errorf("interaction: questions[%q] skip must not carry answer content", item.ID)
		}
		return nil
	default:
		return fmt.Errorf("interaction: questions[%q] has invalid status %q", item.ID, answer.Status)
	}
	if utf8.RuneCountInString(answer.Text) > MaxAnswerTextRunes {
		return fmt.Errorf("interaction: questions[%q].text exceeds %d runes", item.ID, MaxAnswerTextRunes)
	}
	if !utf8.ValidString(answer.Text) ||
		!utf8.ValidString(answer.SelectedOptionID) ||
		!utf8.ValidString(answer.SelectedLabel) {
		return fmt.Errorf("interaction: questions[%q] answer contains invalid UTF-8", item.ID)
	}
	if len(item.Options) == 0 {
		if answer.SelectedOptionID != "" || answer.SelectedLabel != "" {
			return fmt.Errorf("interaction: questions[%q] is free-text and must not select an option", item.ID)
		}
		if strings.TrimSpace(answer.Text) == "" {
			return fmt.Errorf("interaction: questions[%q] needs an answer or an explicit skip", item.ID)
		}
		return nil
	}
	// Option questions accept either a valid selection (with optional
	// supplement text) or a custom answer (both selected fields empty and
	// non-blank text). A half-filled selection stays invalid.
	if answer.SelectedOptionID == "" && answer.SelectedLabel == "" {
		if strings.TrimSpace(answer.Text) == "" {
			return fmt.Errorf("interaction: questions[%q] needs an answer or an explicit skip", item.ID)
		}
		return nil
	}
	var selected *QuestionOption
	for optionIndex := range item.Options {
		if item.Options[optionIndex].ID == answer.SelectedOptionID {
			selected = &item.Options[optionIndex]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf(
			"interaction: questions[%q] selects unknown option %q",
			item.ID,
			answer.SelectedOptionID,
		)
	}
	if answer.SelectedLabel != selected.Label {
		return fmt.Errorf(
			"interaction: questions[%q] label %q does not match option %q",
			item.ID,
			answer.SelectedLabel,
			selected.ID,
		)
	}
	return nil
}
