// Package acp serves AICE Sessions to an Agent Client Protocol client over
// stdio. It is a frontend of the interaction contracts, parallel to the TUI
// and print mode; ACP types never cross into agent or llm.
package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// SessionHost switches the Session a frontend controls. One process serves
// one current Session; session/new and session/load replace it while idle.
type SessionHost interface {
	// StartSession makes a fresh durable Session current and returns its ID.
	StartSession(ctx context.Context) (string, error)
	// LoadSession makes the Session with id current and returns its active
	// branch for replay.
	LoadSession(ctx context.Context, id string) (*interaction.Transcript, error)
}

// Options identifies the served workspace and build.
type Options struct {
	// Workspace is the physical workspace path; sessions must name it as cwd.
	Workspace string
	Version   string
	Host      SessionHost
}

// Serve answers one client until it disconnects or ctx ends.
func Serve(ctx context.Context, frontend interaction.Frontend, options Options, input io.Reader, output io.Writer) error {
	if frontend.Runner == nil || frontend.State == nil || options.Host == nil {
		return errors.New("acp: runner, state and session host are required")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	agent := &agent{frontend: frontend, options: options, ready: make(chan struct{})}
	agent.conn = sdk.NewAgentSideConnection(agent, output, input)
	close(agent.ready)
	if frontend.Guards != nil {
		go agent.serveGuards(ctx, frontend.Guards.GuardRequests())
	}
	if frontend.Titles != nil {
		go agent.serveTitles(ctx, frontend.Titles.SessionTitles())
	}
	select {
	case <-agent.conn.Done():
	case <-ctx.Done():
	}
	agent.cancelPrompt()
	return nil
}

type agent struct {
	frontend interaction.Frontend
	options  Options
	// conn is set once ready closes; the connection may dispatch earlier.
	conn  *sdk.AgentSideConnection
	ready chan struct{}

	mu        sync.Mutex
	sessionID sdk.SessionId
	// cancel stops the running prompt; nil while idle.
	cancel context.CancelFunc
}

var (
	_ sdk.Agent       = (*agent)(nil)
	_ sdk.AgentLoader = (*agent)(nil)
)

// connection returns the connection once the constructor has published it.
func (a *agent) connection() *sdk.AgentSideConnection {
	<-a.ready
	return a.conn
}

func (a *agent) Initialize(context.Context, sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{
		ProtocolVersion: sdk.ProtocolVersionNumber,
		AgentInfo:       &sdk.Implementation{Name: "aice", Title: sdk.Ptr("AICE"), Version: a.options.Version},
		AgentCapabilities: sdk.AgentCapabilities{
			LoadSession:         true,
			PromptCapabilities:  sdk.PromptCapabilities{Image: true},
			SessionCapabilities: sdk.SessionCapabilities{Resume: &sdk.SessionResumeCapabilities{}},
		},
		AuthMethods: []sdk.AuthMethod{},
	}, nil
}

func (a *agent) NewSession(ctx context.Context, params sdk.NewSessionRequest) (sdk.NewSessionResponse, error) {
	if err := a.checkScope(params.Cwd, params.McpServers); err != nil {
		return sdk.NewSessionResponse{}, err
	}
	id, err := a.switchSession(func() (string, error) { return a.options.Host.StartSession(ctx) })
	if err != nil {
		return sdk.NewSessionResponse{}, err
	}
	return sdk.NewSessionResponse{SessionId: id, Modes: a.modes(), ConfigOptions: a.configOptions()}, nil
}

func (a *agent) LoadSession(ctx context.Context, params sdk.LoadSessionRequest) (sdk.LoadSessionResponse, error) {
	if err := a.checkScope(params.Cwd, params.McpServers); err != nil {
		return sdk.LoadSessionResponse{}, err
	}
	var transcript *interaction.Transcript
	id, err := a.switchSession(func() (string, error) {
		loaded, err := a.options.Host.LoadSession(ctx, string(params.SessionId))
		transcript = loaded
		return string(params.SessionId), err
	})
	if err != nil {
		return sdk.LoadSessionResponse{}, err
	}
	if transcript != nil {
		for _, update := range replay(*transcript) {
			if err := a.connection().SessionUpdate(ctx, sdk.SessionNotification{SessionId: id, Update: update}); err != nil {
				return sdk.LoadSessionResponse{}, err
			}
		}
	}
	return sdk.LoadSessionResponse{Modes: a.modes(), ConfigOptions: a.configOptions()}, nil
}

func (a *agent) ResumeSession(ctx context.Context, params sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	if err := a.checkScope(params.Cwd, params.McpServers); err != nil {
		return sdk.ResumeSessionResponse{}, err
	}
	if _, err := a.switchSession(func() (string, error) {
		_, err := a.options.Host.LoadSession(ctx, string(params.SessionId))
		return string(params.SessionId), err
	}); err != nil {
		return sdk.ResumeSessionResponse{}, err
	}
	return sdk.ResumeSessionResponse{Modes: a.modes(), ConfigOptions: a.configOptions()}, nil
}

// checkScope accepts only the served workspace. Client-provided MCP servers
// are not supported: AICE's MCP services come from its own configuration.
func (a *agent) checkScope(cwd string, servers []sdk.McpServer) error {
	if len(servers) > 0 {
		return sdk.NewInvalidParams(map[string]any{"error": "client-provided MCP servers are not supported; configure MCP services in AICE settings"})
	}
	if !sameDirectory(cwd, a.options.Workspace) {
		return sdk.NewInvalidParams(map[string]any{"error": fmt.Sprintf("cwd %q is not the served workspace %q", cwd, a.options.Workspace)})
	}
	return nil
}

// switchSession replaces the current Session while no prompt runs.
func (a *agent) switchSession(open func() (string, error)) (sdk.SessionId, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		return "", sdk.NewInvalidRequest(map[string]any{"error": "a prompt is running; cancel it before switching sessions"})
	}
	id, err := open()
	if err != nil {
		return "", sdk.NewInternalError(map[string]any{"error": err.Error()})
	}
	a.sessionID = sdk.SessionId(id)
	return a.sessionID, nil
}

// serveTitles reports each generated title of the current Session as a
// session_info_update; a title of a Session the client left is not sent.
func (a *agent) serveTitles(ctx context.Context, titles <-chan interaction.SessionTitle) {
	for {
		select {
		case <-ctx.Done():
			return
		case title, ok := <-titles:
			if !ok {
				return
			}
			a.mu.Lock()
			current := a.sessionID
			a.mu.Unlock()
			if string(current) != title.SessionID {
				continue
			}
			_ = a.connection().SessionUpdate(ctx, sdk.SessionNotification{
				SessionId: current,
				Update:    sdk.SessionUpdate{SessionInfoUpdate: &sdk.SessionSessionInfoUpdate{Title: &title.Title}},
			})
		}
	}
}

func (a *agent) requireSession(id sdk.SessionId) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionID == "" || id != a.sessionID {
		return sdk.NewInvalidParams(map[string]any{"error": fmt.Sprintf("session %q is not the current session", id)})
	}
	return nil
}

func (a *agent) Cancel(_ context.Context, params sdk.CancelNotification) error {
	if a.requireSession(params.SessionId) == nil {
		a.cancelPrompt()
	}
	return nil
}

func (a *agent) cancelPrompt() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *agent) SetSessionMode(ctx context.Context, params sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	if err := a.requireSession(params.SessionId); err != nil {
		return sdk.SetSessionModeResponse{}, err
	}
	if a.frontend.PermissionModes == nil {
		return sdk.SetSessionModeResponse{}, sdk.NewMethodNotFound(sdk.AgentMethodSessionSetMode)
	}
	if _, err := a.frontend.PermissionModes.SetPermissionMode(ctx, string(params.ModeId)); err != nil {
		return sdk.SetSessionModeResponse{}, sdk.NewInvalidParams(map[string]any{"error": err.Error()})
	}
	return sdk.SetSessionModeResponse{}, nil
}

// SetSessionConfigOption accepts only the effective model: AICE settings
// choose the model, and ACP declares it so clients can confirm and record it.
func (a *agent) SetSessionConfigOption(_ context.Context, params sdk.SetSessionConfigOptionRequest) (sdk.SetSessionConfigOptionResponse, error) {
	value := params.ValueId
	if value == nil {
		return sdk.SetSessionConfigOptionResponse{}, sdk.NewInvalidParams(map[string]any{"error": "only the model option exists"})
	}
	if err := a.requireSession(value.SessionId); err != nil {
		return sdk.SetSessionConfigOptionResponse{}, err
	}
	current := modelValue(a.frontend.State.RuntimeState().Model)
	if value.ConfigId != modelOptionID || string(value.Value) != current {
		return sdk.SetSessionConfigOptionResponse{}, sdk.NewInvalidParams(map[string]any{
			"error": fmt.Sprintf("the model is chosen by AICE settings (AICE_PROVIDER, AICE_MODEL); current model is %q", current),
		})
	}
	return sdk.SetSessionConfigOptionResponse{ConfigOptions: a.configOptions()}, nil
}

func (a *agent) Authenticate(context.Context, sdk.AuthenticateRequest) (sdk.AuthenticateResponse, error) {
	return sdk.AuthenticateResponse{}, nil
}

func (a *agent) Logout(context.Context, sdk.LogoutRequest) (sdk.LogoutResponse, error) {
	return sdk.LogoutResponse{}, sdk.NewMethodNotFound(sdk.AgentMethodLogout)
}

func (a *agent) CloseSession(context.Context, sdk.CloseSessionRequest) (sdk.CloseSessionResponse, error) {
	return sdk.CloseSessionResponse{}, sdk.NewMethodNotFound(sdk.AgentMethodSessionClose)
}

func (a *agent) ListSessions(context.Context, sdk.ListSessionsRequest) (sdk.ListSessionsResponse, error) {
	return sdk.ListSessionsResponse{}, sdk.NewMethodNotFound(sdk.AgentMethodSessionList)
}

const (
	modelOptionID = "model"
	modeDefault   = "default"
	modeAuto      = "auto"
)

func (a *agent) modes() *sdk.SessionModeState {
	if a.frontend.PermissionModes == nil {
		return nil
	}
	return &sdk.SessionModeState{
		CurrentModeId: sdk.SessionModeId(a.frontend.State.RuntimeState().PermissionMode),
		AvailableModes: []sdk.SessionMode{
			{Id: modeDefault, Name: "Default", Description: sdk.Ptr("Ask before actions the permission rules do not allow")},
			{Id: modeAuto, Name: "Auto", Description: sdk.Ptr("A classifier reviews actions that would ask; denials still apply")},
		},
	}
}

func (a *agent) configOptions() []sdk.SessionConfigOption {
	current := modelValue(a.frontend.State.RuntimeState().Model)
	options := sdk.SessionConfigSelectOptionsUngrouped{{Name: current, Value: sdk.SessionConfigValueId(current)}}
	option := sdk.NewSessionConfigOptionSelect(sdk.SessionConfigValueId(current), sdk.SessionConfigSelectOptions{Ungrouped: &options})
	option.Select.Id = modelOptionID
	option.Select.Name = "Model"
	option.Select.Category = sdk.Ptr(sdk.SessionConfigOptionCategoryModel)
	return []sdk.SessionConfigOption{option}
}

// modelValue names a model as provider/model, the form ACP clients select.
func modelValue(model interaction.DisplayModel) string {
	if model.Provider == "" {
		return model.ID
	}
	return model.Provider + "/" + model.ID
}

func sameDirectory(left, right string) bool {
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	return resolve(left) == resolve(right)
}
