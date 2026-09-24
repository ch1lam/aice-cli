package interaction_test

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func validQuestionRequest() interaction.QuestionRequest {
	return interaction.QuestionRequest{
		ID: "call-1",
		Questions: []interaction.QuestionItem{
			{
				ID:       "execution_mode",
				Header:   "执行方式",
				Question: "直接运行修复命令时，希望采用哪种行为？",
				Options: []interaction.QuestionOption{
					{ID: "check", Label: "仅检查并显示建议", Description: "先审阅，再决定是否修改"},
					{ID: "apply", Label: "直接修改文件", Description: "适合自动修复场景"},
				},
				RecommendedOptionID: "check",
			},
		},
	}
}

func TestValidateQuestionRequestAcceptsSingleChoice(t *testing.T) {
	t.Parallel()
	if err := interaction.ValidateQuestionRequest(validQuestionRequest()); err != nil {
		t.Fatalf("ValidateQuestionRequest() error = %v", err)
	}
}

func TestValidateQuestionRequestRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*interaction.QuestionRequest){
		"empty id": func(r *interaction.QuestionRequest) {
			r.ID = ""
		},
		"no questions": func(r *interaction.QuestionRequest) {
			r.Questions = nil
		},
		"too many questions": func(r *interaction.QuestionRequest) {
			fourth := r.Questions[0]
			fourth.ID = "extra"
			r.Questions = append(r.Questions, r.Questions[0], fourth, fourth)
		},
		"bad question id": func(r *interaction.QuestionRequest) {
			r.Questions[0].ID = "has space"
		},
		"duplicate question id": func(r *interaction.QuestionRequest) {
			r.Questions = append(r.Questions, r.Questions[0])
		},
		"empty question": func(r *interaction.QuestionRequest) {
			r.Questions[0].Question = "   "
		},
		"too many options": func(r *interaction.QuestionRequest) {
			for i := 0; i < 7; i++ {
				r.Questions[0].Options = append(r.Questions[0].Options, interaction.QuestionOption{
					ID:    "opt",
					Label: "x",
				})
			}
			r.Questions[0].Options[0].ID = "opt-0"
			for i := 1; i < len(r.Questions[0].Options); i++ {
				r.Questions[0].Options[i].ID = "opt-" + string(rune('0'+i))
			}
		},
		"duplicate option id": func(r *interaction.QuestionRequest) {
			r.Questions[0].Options = append(r.Questions[0].Options, r.Questions[0].Options[0])
		},
		"dangling recommendation": func(r *interaction.QuestionRequest) {
			r.Questions[0].RecommendedOptionID = "ghost"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := validQuestionRequest()
			mutate(&request)
			if err := interaction.ValidateQuestionRequest(request); err == nil {
				t.Fatalf("ValidateQuestionRequest() = nil, want error for %s", name)
			}
		})
	}
}

func TestValidateQuestionReply(t *testing.T) {
	t.Parallel()
	request := validQuestionRequest()
	answered := interaction.QuestionReply{
		RequestID: "call-1",
		Answers: map[string]interaction.QuestionAnswer{
			"execution_mode": {
				Status:           interaction.QuestionAnswered,
				SelectedOptionID: "check",
				SelectedLabel:    "仅检查并显示建议",
				Text:             "只允许修改当前目录",
			},
		},
	}
	if err := interaction.ValidateQuestionReply(request, answered); err != nil {
		t.Fatalf("ValidateQuestionReply() error = %v", err)
	}

	skipped := interaction.QuestionReply{
		RequestID: "call-1",
		Answers: map[string]interaction.QuestionAnswer{
			"execution_mode": {Status: interaction.QuestionSkipped},
		},
	}
	if err := interaction.ValidateQuestionReply(request, skipped); err != nil {
		t.Fatalf("ValidateQuestionReply() error = %v", err)
	}

	cases := map[string]interaction.QuestionReply{
		"wrong request": {
			RequestID: "call-2",
			Answers:   answered.Answers,
		},
		"missing answer": {
			RequestID: "call-1",
			Answers:   map[string]interaction.QuestionAnswer{},
		},
		"unknown question": {
			RequestID: "call-1",
			Answers: map[string]interaction.QuestionAnswer{
				"execution_mode": {Status: interaction.QuestionSkipped},
				"ghost":          {Status: interaction.QuestionSkipped},
			},
		},
		"unknown option": {
			RequestID: "call-1",
			Answers: map[string]interaction.QuestionAnswer{
				"execution_mode": {
					Status:           interaction.QuestionAnswered,
					SelectedOptionID: "ghost",
					SelectedLabel:    "ghost",
				},
			},
		},
		"label mismatch": {
			RequestID: "call-1",
			Answers: map[string]interaction.QuestionAnswer{
				"execution_mode": {
					Status:           interaction.QuestionAnswered,
					SelectedOptionID: "check",
					SelectedLabel:    "直接修改文件",
				},
			},
		},
		"skip with content": {
			RequestID: "call-1",
			Answers: map[string]interaction.QuestionAnswer{
				"execution_mode": {Status: interaction.QuestionSkipped, Text: "nope"},
			},
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := interaction.ValidateQuestionReply(request, reply); err == nil {
				t.Fatalf("ValidateQuestionReply() = nil, want error for %s", name)
			}
		})
	}
}

func TestValidateQuestionReplyCustomText(t *testing.T) {
	t.Parallel()
	request := validQuestionRequest()
	// Option questions accept a custom answer: empty selection, non-blank text.
	custom := interaction.QuestionReply{
		RequestID: "call-1",
		Answers: map[string]interaction.QuestionAnswer{
			"execution_mode": {
				Status: interaction.QuestionAnswered,
				Text:   "先做最小实现，不增加新依赖",
			},
		},
	}
	if err := interaction.ValidateQuestionReply(request, custom); err != nil {
		t.Fatalf("ValidateQuestionReply() error = %v, want custom text to validate", err)
	}
	cases := map[string]interaction.QuestionAnswer{
		"blank custom": {
			Status: interaction.QuestionAnswered,
			Text:   "   ",
		},
		"half-filled id": {
			Status:           interaction.QuestionAnswered,
			SelectedOptionID: "check",
			Text:             "补充说明",
		},
		"half-filled label": {
			Status:        interaction.QuestionAnswered,
			SelectedLabel: "仅检查并显示建议",
			Text:          "补充说明",
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reply := interaction.QuestionReply{
				RequestID: "call-1",
				Answers:   map[string]interaction.QuestionAnswer{"execution_mode": answer},
			}
			if err := interaction.ValidateQuestionReply(request, reply); err == nil {
				t.Fatalf("ValidateQuestionReply() = nil, want error for %s", name)
			}
		})
	}
}

func TestValidateQuestionReplyFreeText(t *testing.T) {
	t.Parallel()
	request := interaction.QuestionRequest{
		ID: "call-9",
		Questions: []interaction.QuestionItem{
			{ID: "goal", Question: "这次修复的目标是什么？"},
		},
	}
	if err := interaction.ValidateQuestionRequest(request); err != nil {
		t.Fatalf("ValidateQuestionRequest() error = %v", err)
	}
	blank := interaction.QuestionReply{
		RequestID: "call-9",
		Answers: map[string]interaction.QuestionAnswer{
			"goal": {Status: interaction.QuestionAnswered},
		},
	}
	if err := interaction.ValidateQuestionReply(request, blank); err == nil {
		t.Fatal("ValidateQuestionReply() = nil for blank free-text answer, want error")
	}
	good := interaction.QuestionReply{
		RequestID: "call-9",
		Answers: map[string]interaction.QuestionAnswer{
			"goal": {Status: interaction.QuestionAnswered, Text: "修复 flaky 测试"},
		},
	}
	if err := interaction.ValidateQuestionReply(request, good); err != nil {
		t.Fatalf("ValidateQuestionReply() error = %v", err)
	}
}
