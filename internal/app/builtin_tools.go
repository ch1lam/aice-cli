package app

import (
	"fmt"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func newBuiltInTools(workspace *tool.Workspace) ([]agent.Tool, error) {
	if workspace == nil {
		return nil, fmt.Errorf("app: workspace is required")
	}
	// A tool whose external executable is missing becomes an unavailable stub
	// instead of failing the whole tool set, so the app always starts and the
	// agent can explain the gap to the user.
	tools := make([]agent.Tool, 0, 7)
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
	bash, err := tool.NewBash(workspace)
	add("bash", bash, err)
	grep, err := tool.NewGrep(workspace)
	add("grep", grep, err)
	find, err := tool.NewFind(workspace)
	add("find", find, err)
	ls, err := tool.NewLS(workspace)
	add("ls", ls, err)
	return tools, nil
}
