package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func newBuiltInTools(ctx context.Context, workspace *tool.Workspace, interactive bool) ([]agent.Tool, error) {
	if workspace == nil {
		return nil, fmt.Errorf("app: workspace is required")
	}
	// A tool whose external executable is missing becomes an unavailable stub
	// instead of failing the whole tool set, so the app always starts and the
	// agent can explain the gap to the user.
	tools := make([]agent.Tool, 0, 8)
	add := func(name string, current agent.Tool, err error) {
		if err != nil {
			current = tool.NewUnavailable(name, err)
		}
		tools = append(tools, current)
	}

	read, err := tool.NewRead(workspace)
	add("read", read, err)
	write, err := tool.NewWrite(workspace)
	add("write", write, err)
	edit, err := tool.NewEdit(workspace)
	add("edit", edit, err)
	bash, err := tool.NewBash(ctx, workspace)
	add("bash", bash, err)
	grep, err := tool.NewGrep(workspace)
	add("grep", grep, err)
	find, err := tool.NewFind(workspace)
	add("find", find, err)
	ls, err := tool.NewLS(workspace)
	add("ls", ls, err)
	// request_user_input is interactive-only: --print and Harbor never see
	// its schema, so a non-interactive model states missing information in
	// its output instead of waiting on terminal input. The asker stays
	// unbound until the interactive Session exists (see bindQuestionTool).
	if interactive {
		tools = append(tools, tool.NewRequestUserInput(nil))
	}
	return tools, nil
}
