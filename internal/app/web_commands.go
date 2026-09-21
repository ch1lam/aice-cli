package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/web"
	"github.com/ch1lam/aice-cli/internal/web/exa"
)

// webIntro is shown at the top of /web prompts.
const webIntro = "Web search sends queries to the configured service; web_fetch connects directly to public sites. " +
	"Every call still asks for permission unless granted for this Session. Manual configuration lives under \"web\" in the global settings file."

func (s *interactiveSession) webMenu() *interaction.CommandMenu {
	settings := s.settingsSnapshot().configuration.Web
	return &interaction.CommandMenu{Title: "Web search and fetch", Options: []interaction.CommandOption{
		{Label: "Status", Description: "Effective sources, priority and reasons", Arguments: "status"},
		{Label: "Add Exa instance…", Description: "Configure an Exa Search API account and append it to the priority list", Arguments: "add"},
		{Label: "Set instance credential…", Description: "Replace the API key or environment reference of an instance", Arguments: "credential"},
		{Label: "Remove instance…", Description: "Delete an instance and its priority entry", Arguments: "remove"},
		{Label: "Move source up…", Arguments: "up"},
		{Label: "Move source down…", Arguments: "down"},
		{Label: "Remove source from priority…", Description: "native may be removed too; an empty list allows no search", Arguments: "drop"},
		{Label: "Add source to priority…", Description: "Append native or a configured instance", Arguments: "append"},
		{Label: "Web search: " + onOff(settings.SearchEnabled) + " (toggle)", Arguments: "search"},
		{Label: "Web fetch: " + onOff(settings.FetchEnabled) + " (toggle)", Description: "Direct connections, no authentication, HTML/text/Markdown only", Arguments: "fetch"},
	}}
}

func (s *interactiveSession) slashWeb(ctx context.Context, request interaction.CommandRequest) (string, error) {
	action := strings.TrimSpace(request.Arguments)
	if action == "" || action == "status" {
		return s.webStatus(), nil
	}
	s.conversation.historyMu.RLock()
	active := s.conversation.activeMainRun != nil
	s.conversation.historyMu.RUnlock()
	if active {
		return "", fmt.Errorf("app: cannot change web settings while a response is running")
	}
	switch action {
	case "search":
		enabled := !s.settingsSnapshot().configuration.Web.SearchEnabled
		return s.saveWeb(ctx, config.WebPatch{SearchEnabled: &enabled}, "Web search: "+onOff(enabled)+" (saved)")
	case "fetch":
		enabled := !s.settingsSnapshot().configuration.Web.FetchEnabled
		return s.saveWeb(ctx, config.WebPatch{FetchEnabled: &enabled}, "Web fetch: "+onOff(enabled)+" (saved)")
	}
	if request.Auth == nil || request.Auth.Notify == nil {
		return "", fmt.Errorf("app: /web %s requires the interactive menu", action)
	}
	switch action {
	case "add":
		return s.webAddInstance(ctx, request.Auth)
	case "credential":
		return s.webSetCredential(ctx, request.Auth)
	case "remove":
		return s.webRemoveInstance(ctx, request.Auth)
	case "up", "down", "drop":
		return s.webReorder(ctx, request.Auth, action)
	case "append":
		return s.webAppendSource(ctx, request.Auth)
	default:
		return "", fmt.Errorf("app: unknown web action %q", action)
	}
}

func (s *interactiveSession) webStatus() string {
	s.stateMu.RLock()
	state := s.web
	s.stateMu.RUnlock()
	lines := append([]string{"Web"}, state.statusLines()...)
	if path := s.settingsSnapshot().configuration.Paths.GlobalSettings; path != "" {
		lines = append(lines, "Configured in: "+path+" (\"web\" object)")
	}
	return strings.Join(lines, "\n")
}

// webSummary is the one-line /settings entry.
func (s webState) summary() string {
	source := "none"
	switch {
	case s.configErr != nil:
		source = "error (see /web)"
	case s.binding.Kind == web.BindingService:
		source = s.binding.Selected.Entry
	}
	return fmt.Sprintf("Web: search %s (source %s), fetch %s; details in /web", onOff(s.settings.SearchEnabled), source, onOff(s.settings.FetchEnabled))
}

func webPrompt(ctx context.Context, ui *interaction.AuthInteraction, prompt interaction.AuthPrompt) (string, error) {
	if prompt.Instructions == "" {
		prompt.Instructions = webIntro
	}
	if err := ui.Notify(ctx, prompt); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case value, ok := <-ui.Input:
		if !ok {
			return "", fmt.Errorf("app: web settings input closed")
		}
		return value, nil
	}
}

func webMenuPrompt(ctx context.Context, ui *interaction.AuthInteraction, title, instructions string, options []interaction.CommandOption) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("app: %s: nothing to choose", strings.ToLower(title))
	}
	return webPrompt(ctx, ui, interaction.AuthPrompt{Title: title, Instructions: instructions, Menu: &interaction.CommandMenu{Title: title, Options: options}})
}

// nextExaInstanceID picks a free identifier so the user never types one into a
// hidden prompt; the ID can be renamed in the settings file.
func nextExaInstanceID(existing map[string]config.WebService) string {
	if _, taken := existing["exa-main"]; !taken {
		return "exa-main"
	}
	for index := 2; ; index++ {
		id := fmt.Sprintf("exa-%d", index)
		if _, taken := existing[id]; !taken {
			return id
		}
	}
}

func (s *interactiveSession) webAddInstance(ctx context.Context, ui *interaction.AuthInteraction) (string, error) {
	current := s.settingsSnapshot().configuration
	id := nextExaInstanceID(current.Web.Services)
	choice, err := webMenuPrompt(ctx, ui, "Add Exa instance "+id,
		"Endpoint: "+exa.DefaultBaseURL+" (edit base_url in settings.json for a gateway). Choose where the API key comes from.",
		[]interaction.CommandOption{
			{Label: "Enter an API key now (saved to the auth store)", Arguments: "key"},
			{Label: "Read the key from environment variable EXA_API_KEY", Arguments: "env"},
		})
	if err != nil {
		return "", err
	}
	service := config.WebServiceSettings{Provider: exa.ProviderID, API: exa.APIID}
	credentialNote := ""
	secret := ""
	switch choice {
	case "env":
		service.Credential = config.WebCredentialRef{Env: "EXA_API_KEY"}
	case "key":
		secret, err = webPrompt(ctx, ui, interaction.AuthPrompt{Title: "Exa API key for " + id, AllowInput: true, InputLabel: "Exa API key (input hidden)"})
		if err != nil {
			return "", err
		}
		secret = strings.TrimSpace(secret)
		if secret == "" || strings.ContainsAny(secret, "\r\n") {
			return "", fmt.Errorf("app: Exa API key must be one non-empty line")
		}
		service.Credential = config.WebCredentialRef{AuthRef: config.WebAuthRefPrefix + id}
	default:
		return "", fmt.Errorf("app: unknown credential choice %q", choice)
	}
	if secret != "" {
		if err := config.SaveWebCredentialFile(ctx, current.Paths, id, secret); err != nil {
			return "", fmt.Errorf("app: save Exa credential: %w", err)
		}
		credentialNote = "credential saved to " + current.Paths.GlobalAuth
		s.stateMu.Lock()
		s.configuration = s.configuration.WithWebCredential(id, secret)
		s.stateMu.Unlock()
	}
	priority := append(slices.Clone(current.Web.Priority), web.ServiceEntry(id))
	message, err := s.saveWeb(ctx, config.WebPatch{Services: map[string]*config.WebServiceSettings{id: &service}, Priority: &priority},
		fmt.Sprintf("Added Exa instance %s and appended %s to the priority list.", id, web.ServiceEntry(id)))
	if err != nil {
		if credentialNote != "" {
			return "", fmt.Errorf("%s, but the instance was not saved and the current Session is unchanged: %w", credentialNote, err)
		}
		return "", err
	}
	if credentialNote != "" {
		message += "\n" + credentialNote
	}
	return message, nil
}

func (s *interactiveSession) webInstanceChoice(ctx context.Context, ui *interaction.AuthInteraction, title string) (string, error) {
	current := s.settingsSnapshot().configuration.Web
	options := make([]interaction.CommandOption, 0, len(current.Services))
	for _, id := range current.ServiceIDs() {
		service := current.Services[id]
		options = append(options, interaction.CommandOption{Label: id, Description: service.Provider + " · " + service.CredentialDescription(), Arguments: id})
	}
	return webMenuPrompt(ctx, ui, title, "", options)
}

func (s *interactiveSession) webSetCredential(ctx context.Context, ui *interaction.AuthInteraction) (string, error) {
	id, err := s.webInstanceChoice(ctx, ui, "Choose instance")
	if err != nil {
		return "", err
	}
	current := s.settingsSnapshot().configuration
	service, ok := current.Web.Services[id]
	if !ok {
		return "", fmt.Errorf("app: instance %q is not configured", id)
	}
	choice, err := webMenuPrompt(ctx, ui, "Credential for "+id, "", []interaction.CommandOption{
		{Label: "Enter a new API key (saved to the auth store)", Arguments: "key"},
		{Label: "Read the key from environment variable EXA_API_KEY", Arguments: "env"},
	})
	if err != nil {
		return "", err
	}
	updated := config.WebServiceSettings{Provider: service.Provider, API: service.API, BaseURL: service.BaseURL, Options: service.Options}
	if !service.Enabled {
		enabled := false
		updated.Enabled = &enabled
	}
	note := ""
	switch choice {
	case "env":
		updated.Credential = config.WebCredentialRef{Env: "EXA_API_KEY"}
	case "key":
		secret, err := webPrompt(ctx, ui, interaction.AuthPrompt{Title: "API key for " + id, AllowInput: true, InputLabel: "API key (input hidden)"})
		if err != nil {
			return "", err
		}
		secret = strings.TrimSpace(secret)
		if secret == "" || strings.ContainsAny(secret, "\r\n") {
			return "", fmt.Errorf("app: API key must be one non-empty line")
		}
		if err := config.SaveWebCredentialFile(ctx, current.Paths, id, secret); err != nil {
			return "", fmt.Errorf("app: save credential: %w", err)
		}
		s.stateMu.Lock()
		s.configuration = s.configuration.WithWebCredential(id, secret)
		s.stateMu.Unlock()
		updated.Credential = config.WebCredentialRef{AuthRef: config.WebAuthRefPrefix + id}
		note = "credential saved to " + current.Paths.GlobalAuth
	default:
		return "", fmt.Errorf("app: unknown credential choice %q", choice)
	}
	message, err := s.saveWeb(ctx, config.WebPatch{Services: map[string]*config.WebServiceSettings{id: &updated}}, "Updated credential reference for "+id+".")
	if err != nil {
		if note != "" {
			return "", fmt.Errorf("%s, but the instance was not updated and the current Session is unchanged: %w", note, err)
		}
		return "", err
	}
	if note != "" {
		message += "\n" + note
	}
	return message, nil
}

func (s *interactiveSession) webRemoveInstance(ctx context.Context, ui *interaction.AuthInteraction) (string, error) {
	id, err := s.webInstanceChoice(ctx, ui, "Remove instance")
	if err != nil {
		return "", err
	}
	current := s.settingsSnapshot().configuration
	service, ok := current.Web.Services[id]
	if !ok {
		return "", fmt.Errorf("app: instance %q is not configured", id)
	}
	priority := slices.DeleteFunc(slices.Clone(current.Web.Priority), func(entry string) bool { return entry == web.ServiceEntry(id) })
	message, err := s.saveWeb(ctx, config.WebPatch{Services: map[string]*config.WebServiceSettings{id: nil}, Priority: &priority}, "Removed instance "+id+".")
	if err != nil {
		return "", err
	}
	if service.Credential.AuthRef != "" {
		if err := config.SaveWebCredentialFile(ctx, current.Paths, id, ""); err != nil {
			return message + "\nStored credential could not be removed: " + err.Error(), nil
		}
		s.stateMu.Lock()
		s.configuration = s.configuration.WithWebCredential(id, "")
		s.stateMu.Unlock()
		message += "\nStored credential removed from " + current.Paths.GlobalAuth
	}
	return message, nil
}

func (s *interactiveSession) webReorder(ctx context.Context, ui *interaction.AuthInteraction, action string) (string, error) {
	current := s.settingsSnapshot().configuration.Web
	options := make([]interaction.CommandOption, 0, len(current.Priority))
	for index, entry := range current.Priority {
		options = append(options, interaction.CommandOption{Label: fmt.Sprintf("%d. %s", index+1, entry), Arguments: entry})
	}
	titles := map[string]string{"up": "Move source up", "down": "Move source down", "drop": "Remove source from priority"}
	entry, err := webMenuPrompt(ctx, ui, titles[action], "", options)
	if err != nil {
		return "", err
	}
	priority := slices.Clone(current.Priority)
	index := slices.Index(priority, entry)
	if index < 0 {
		return "", fmt.Errorf("app: %q is not in the priority list", entry)
	}
	switch action {
	case "up":
		if index == 0 {
			return entry + " is already first.", nil
		}
		priority[index-1], priority[index] = priority[index], priority[index-1]
	case "down":
		if index == len(priority)-1 {
			return entry + " is already last.", nil
		}
		priority[index+1], priority[index] = priority[index], priority[index+1]
	case "drop":
		priority = slices.Delete(priority, index, index+1)
	}
	return s.saveWeb(ctx, config.WebPatch{Priority: &priority}, "Priority: "+formatPriority(priority))
}

func (s *interactiveSession) webAppendSource(ctx context.Context, ui *interaction.AuthInteraction) (string, error) {
	current := s.settingsSnapshot().configuration.Web
	var options []interaction.CommandOption
	if !slices.Contains(current.Priority, web.PriorityNative) {
		options = append(options, interaction.CommandOption{Label: web.PriorityNative, Description: "model-native search (not implemented in this version)", Arguments: web.PriorityNative})
	}
	for _, id := range current.ServiceIDs() {
		if entry := web.ServiceEntry(id); !slices.Contains(current.Priority, entry) {
			options = append(options, interaction.CommandOption{Label: entry, Description: current.Services[id].Provider, Arguments: entry})
		}
	}
	entry, err := webMenuPrompt(ctx, ui, "Add source to priority", "", options)
	if err != nil {
		return "", err
	}
	priority := append(slices.Clone(current.Priority), entry)
	return s.saveWeb(ctx, config.WebPatch{Priority: &priority}, "Priority: "+formatPriority(priority))
}

func formatPriority(priority []string) string {
	if len(priority) == 0 {
		return "[] (no search source allowed)"
	}
	return strings.Join(priority, ", ")
}

// saveWeb persists one patch, publishes the new snapshot and rebinds the web
// tools for the next run. A failed save leaves the current snapshot untouched.
func (s *interactiveSession) saveWeb(ctx context.Context, patch config.WebPatch, message string) (string, error) {
	if s.application == nil {
		return "", fmt.Errorf("app: application is required")
	}
	current := s.settingsSnapshot().configuration
	saved, err := s.application.dependencies.saveWebSettings(ctx, current.Paths, patch)
	if err != nil {
		return "", fmt.Errorf("app: save web settings; current Session unchanged: %w", err)
	}
	if err := s.applyWebSettings(saved); err != nil {
		return "", err
	}
	s.stateMu.RLock()
	state := s.web
	s.stateMu.RUnlock()
	lines := []string{message, "Applies to the next response."}
	if state.configErr != nil {
		lines = append(lines, "Web search unavailable: "+state.configErr.Error())
	} else if state.binding.Kind == web.BindingService {
		lines = append(lines, "Search source: "+state.binding.Selected.Entry)
	} else if state.settings.SearchEnabled {
		lines = append(lines, "Search source: none — "+state.binding.Reason)
	}
	return strings.Join(lines, "\n"), nil
}

// applyWebSettings publishes a saved web configuration: new snapshot, rebound
// tools, refreshed prompt, rebuilt loop and Guard fingerprint. Callers ensure
// no main run is active so an approved call never targets another service.
func (s *interactiveSession) applyWebSettings(saved config.WebSettings) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	configuration, err := s.configuration.WithWeb(saved)
	if err != nil {
		return fmt.Errorf("app: apply web settings: %w", err)
	}
	state := bindWeb(s.application.webBackends(), configuration.Web)
	tools := append(slices.Clip(s.baseTools), state.tools()...)
	systemPrompt, err := assembleSystemPrompt(s.workspace, configuration, s.trustDecision, tools, s.skills)
	if err != nil {
		state.closeBackend()
		return fmt.Errorf("app: rebuild system prompt: %w", err)
	}
	loop := s.loop
	if s.modelErr == nil && providerConfigured(s.providers, configuration) {
		loop, err = s.application.newAgentLoopWithOptions(configuration, tools, agent.WithGuard(s.guardAdapter), agent.WithGuardAskHandler(s.handleGuardAsk))
		if err != nil {
			state.closeBackend()
			return err
		}
	}
	s.web.closeBackend()
	s.web = state
	s.configuration = configuration
	s.tools = tools
	s.systemPrompt = systemPrompt
	s.loop = loop
	s.guard.SetSearchTarget(state.searchTarget)
	return nil
}
