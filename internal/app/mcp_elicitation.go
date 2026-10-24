package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/utf8text"
)

// An elicitation form is shown in pages of the question panel's size.
const maxMCPElicitationFields = 4 * interaction.MaxQuestionsPerRequest

type mcpElicitorKey struct{}

// mcpElicitor answers a service's request for user input during an operation.
// Only interactive runs install one; otherwise the request is cancelled.
type mcpElicitor func(context.Context, string, mcpclient.Elicitation) (mcpclient.ElicitationReply, error)

func withMCPElicitor(ctx context.Context, elicit mcpElicitor) context.Context {
	return context.WithValue(ctx, mcpElicitorKey{}, elicit)
}

// mcpElicit binds a connection to the service identity shown to the user and
// routes each request to the elicitor of the operation's run.
func mcpElicit(service string) mcpclient.ElicitFunc {
	return func(ctx context.Context, request mcpclient.Elicitation) (mcpclient.ElicitationReply, error) {
		elicit, _ := ctx.Value(mcpElicitorKey{}).(mcpElicitor)
		if elicit == nil {
			return mcpclient.ElicitationReply{Action: mcpclient.ElicitationCancel}, nil
		}
		return elicit(ctx, service, request)
	}
}

// mcpElicitationField is one primitive property of a form-mode schema.
type mcpElicitationField struct {
	name, kind, title, description string
	required                       bool
	values                         []mcpElicitationValue
}

type mcpElicitationValue struct{ value, label string }

func parseMCPElicitationSchema(raw json.RawMessage) ([]mcpElicitationField, error) {
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type        string   `json:"type"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
			EnumNames   []string `json:"enumNames"`
			OneOf       []struct {
				Const string `json:"const"`
				Title string `json:"title"`
			} `json:"oneOf"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil || schema.Type != "object" || len(schema.Properties) > maxMCPElicitationFields {
		return nil, fmt.Errorf("MCP elicitation form must be an object of at most %d fields", maxMCPElicitationFields)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	fields := make([]mcpElicitationField, 0, len(names))
	for _, name := range names {
		property := schema.Properties[name]
		field := mcpElicitationField{name: name, kind: property.Type, title: property.Title, description: property.Description, required: slices.Contains(schema.Required, name)}
		switch property.Type {
		case "string", "number", "integer", "boolean":
		default:
			return nil, fmt.Errorf("MCP elicitation field %q has unsupported type %q", name, property.Type)
		}
		for i, value := range property.Enum {
			label := value
			if i < len(property.EnumNames) && property.EnumNames[i] != "" {
				label = property.EnumNames[i]
			}
			field.values = append(field.values, mcpElicitationValue{value, label})
		}
		for _, option := range property.OneOf {
			field.values = append(field.values, mcpElicitationValue{option.Const, cmp.Or(option.Title, option.Const)})
		}
		if property.Type == "boolean" {
			field.values = []mcpElicitationValue{{"true", "Yes"}, {"false", "No"}}
		}
		fields = append(fields, field)
	}
	return fields, nil
}

var mcpElicitationRequests atomic.Uint64

// elicitMCP asks one page of the question panel per three fields. A skipped
// required field declines the whole request; nothing is sent until every page
// is answered. Server text is labelled untrusted and never enters the Session.
func (s *interactiveSession) elicitMCP(ctx context.Context, service string, request mcpclient.Elicitation) (mcpclient.ElicitationReply, error) {
	cancel := mcpclient.ElicitationReply{Action: mcpclient.ElicitationCancel}
	if s == nil || s.questionRequests == nil {
		return cancel, nil
	}
	fields, err := parseMCPElicitationSchema(request.Schema)
	if err != nil {
		return cancel, err
	}
	content := make(map[string]any)
	intro := fmt.Sprintf("MCP service %s requests input (untrusted server text):\n%s\n\nDo not enter passwords, tokens or other secrets. Skip a required field to decline.", service, request.Message)
	if len(fields) == 0 {
		fields = []mcpElicitationField{{name: "", kind: "confirm", values: []mcpElicitationValue{{"accept", "Accept"}, {"decline", "Decline"}}, required: true}}
	}
	for start := 0; start < len(fields); start += interaction.MaxQuestionsPerRequest {
		page := fields[start:min(start+interaction.MaxQuestionsPerRequest, len(fields))]
		questions := make([]interaction.QuestionItem, len(page))
		for i, field := range page {
			questions[i] = mcpElicitationQuestion(i, field, intro, service)
			intro = ""
		}
		reply, err := s.AskQuestion(ctx, interaction.QuestionRequest{ID: fmt.Sprintf("mcp-elicitation-%d", mcpElicitationRequests.Add(1)), Questions: questions})
		if err != nil {
			return cancel, err
		}
		for i, field := range page {
			answer := reply.Answers[questions[i].ID]
			value := answer.Text
			if answer.SelectedOptionID != "" {
				index, _ := strconv.Atoi(strings.TrimPrefix(answer.SelectedOptionID, "v"))
				value = field.values[index].value
			}
			if answer.Status != interaction.QuestionAnswered || value == "" {
				if field.required {
					return mcpclient.ElicitationReply{Action: mcpclient.ElicitationDecline}, nil
				}
				continue
			}
			if field.kind == "confirm" {
				if value != "accept" {
					return mcpclient.ElicitationReply{Action: mcpclient.ElicitationDecline}, nil
				}
				continue
			}
			typed, err := mcpElicitationValueOf(field, value)
			if err != nil {
				return cancel, err
			}
			content[field.name] = typed
		}
	}
	return mcpclient.ElicitationReply{Action: mcpclient.ElicitationAccept, Content: content}, nil
}

func mcpElicitationQuestion(index int, field mcpElicitationField, intro, service string) interaction.QuestionItem {
	label := cmp.Or(field.title, field.name)
	text := label
	if field.kind == "confirm" {
		text = "Accept this request?"
	}
	if field.description != "" {
		text += " — " + field.description
	}
	if field.required {
		text += " (required)"
	}
	if len(field.values) > interaction.MaxOptionsPerQuestion {
		allowed := make([]string, len(field.values))
		for i, value := range field.values {
			allowed[i] = value.value
		}
		text += "\nAllowed values: " + strings.Join(allowed, ", ")
	}
	if intro != "" {
		text = intro + "\n\n" + text
	}
	question := interaction.QuestionItem{
		ID:       fmt.Sprintf("f%d", index),
		Header:   utf8text.Prefix("MCP "+service, interaction.MaxQuestionHeaderRunes),
		Question: utf8text.Prefix(text, interaction.MaxQuestionTextRunes),
	}
	if len(field.values) <= interaction.MaxOptionsPerQuestion {
		for i, value := range field.values {
			question.Options = append(question.Options, interaction.QuestionOption{ID: fmt.Sprintf("v%d", i), Label: utf8text.Prefix(cmp.Or(value.label, value.value), interaction.MaxOptionLabelRunes)})
		}
	}
	return question
}

func mcpElicitationValueOf(field mcpElicitationField, value string) (any, error) {
	switch field.kind {
	case "boolean":
		if value != "true" && value != "false" {
			return nil, fmt.Errorf("MCP elicitation field %q needs Yes or No", field.name)
		}
		return value == "true", nil
	case "integer":
		number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("MCP elicitation field %q needs an integer", field.name)
		}
		return number, nil
	case "number":
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("MCP elicitation field %q needs a number", field.name)
		}
		return number, nil
	}
	return value, nil
}
