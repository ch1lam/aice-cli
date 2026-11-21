package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// newBuiltInTools builds the host tools. Non-nil tasks enable background bash
// commands and the task tools that observe and stop them.
func newBuiltInTools(ctx context.Context, workspace *tool.Workspace, tasks *tool.Tasks, questions bool) ([]agent.Tool, error) {
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
	bash, err := tool.NewBash(ctx, workspace, hostBashShells(), tasks)
	add("bash", bash, err)
	// Background subagents need the task tools even where bash cannot run
	// commands in the background.
	if tasks != nil {
		output, err := tool.NewTaskOutput(tasks)
		add("task_output", output, err)
		kill, err := tool.NewTaskKill(tasks)
		add("task_kill", kill, err)
	}
	grep, err := tool.NewGrep(workspace)
	add("grep", grep, err)
	find, err := tool.NewFind(workspace)
	add("find", find, err)
	ls, err := tool.NewLS(workspace)
	add("ls", ls, err)
	// request_user_input exists only for a frontend that answers questions:
	// --print and ACP never see its schema, so the model states missing
	// information in its output instead. The asker stays unbound until the
	// interactive Session exists (see bindQuestionTool).
	if questions {
		tools = append(tools, tool.NewRequestUserInput(nil))
	}
	return tools, nil
}

// taskNotifications reports background tasks that finished since the last
// report, as AICE notifications the Loop takes at its next boundary.
func taskNotifications(tasks *tool.Tasks) agent.InputSource {
	if tasks == nil {
		return nil
	}
	return func() (agent.InputMessage, bool, error) {
		message, id, ok := tasks.TakeNotice()
		return agent.InputMessage{ID: id, Message: message}, ok, nil
	}
}

// awaitSubagents keeps a run that would end while subagents still run: it
// waits for the next notification, or reports none once no subagent runs.
// A frontend that cannot start a run on its own, such as --print or ACP,
// would otherwise lose their reports. Background commands are not awaited:
// a server never finishes.
func awaitSubagents(tasks *tool.Tasks) func(context.Context) (agent.InputMessage, bool, error) {
	if tasks == nil {
		return nil
	}
	take := taskNotifications(tasks)
	return func(ctx context.Context) (agent.InputMessage, bool, error) {
		for {
			changed := tasks.Changed()
			if input, ok, err := take(); ok || err != nil {
				return input, ok, err
			}
			if tasks.RunningKind(tool.TaskAgent) == 0 {
				// The last subagent may have finished after take looked.
				select {
				case <-changed:
					continue
				default:
					return agent.InputMessage{}, false, nil
				}
			}
			select {
			case <-changed:
			case <-ctx.Done():
				return agent.InputMessage{}, false, ctx.Err()
			}
		}
	}
}

// hostBashShells resolves the bash tool's shell: native Bash, otherwise the
// WSL launchers the tool probes as a fallback.
func hostBashShells() tool.BashShells {
	opts := deps.DefaultOptions()
	native, err := deps.FindNativeBash(opts)
	if err != nil {
		return tool.BashShells{NativeErr: err, WSL: deps.WSLBashPaths(opts)}
	}
	return tool.BashShells{Native: native}
}

// composeTools is shared by startup, Web replacement and Settings publication.
// Optional capabilities never become part of the host baseTools.
func composeTools(base []agent.Tool, web webState, configuration config.Config) ([]agent.Tool, error) {
	result := append(slices.Clone(base), web.tools()...)
	readResult, err := tool.NewToolResultRead(runResultReader{})
	if err != nil {
		return nil, err
	}
	delegate, err := tool.NewAgent(delegationRouter{})
	if err != nil {
		return nil, err
	}
	message, err := tool.NewSendMessage(messageRouter{})
	if err != nil {
		return nil, err
	}
	result = append(result, readResult, delegate, message)
	if len(configuration.MCP.Servers) == 0 {
		return result, nil
	}
	search, err := tool.NewToolSearch(mcpRunRouter{})
	if err != nil {
		return nil, err
	}
	resources, err := tool.NewMCPResourceList(mcpRunRouter{})
	if err != nil {
		return nil, err
	}
	info, err := tool.NewMCPInfo(mcpRunRouter{})
	if err != nil {
		return nil, err
	}
	return append(result, search, resources, info), nil
}
