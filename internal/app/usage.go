package app

import (
	"context"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func (s *interactiveSession) ReadUsage(ctx context.Context) (interaction.UsageSnapshot, error) {
	_, err := s.beginPreparation()
	if err != nil {
		return interaction.UsageSnapshot{}, err
	}
	defer s.endPreparation()
	if err := ctx.Err(); err != nil {
		return interaction.UsageSnapshot{}, err
	}
	settings := s.settingsSnapshot()
	revision, _ := s.settingsStatus()
	result := interaction.UsageSnapshot{Revision: revision, ReadAt: time.Now(), Directory: s.workspacePath,
		Provider: string(settings.model.Provider), Model: settings.model.ID,
		Context:      s.contextSnapshotFor(settings.model, settings.configuration, settings.systemPrompt),
		WindowSource: contextWindowInformation(settings.model, settings.configuration), CostStatus: "Unavailable",
		Scope: "Recorded usage across all branches and compactions; excludes temporary BTW answers. Interrupted requests may be unreported."}
	s.conversation.historySyncMu.Lock()
	defer s.conversation.historySyncMu.Unlock()
	store := s.conversation.store
	if store == nil {
		return result, nil
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		return result, err
	}
	result.SessionID, result.Path, result.LeafID = snapshot.Header.ID, store.Path(), snapshot.LeafID
	result.Directory, result.CreatedAt = snapshot.Header.WorkingDirectory, snapshot.Header.CreatedAt
	result.Nodes, result.Messages, result.Compactions = len(snapshot.Messages)+len(snapshot.Compactions), len(snapshot.Messages), len(snapshot.Compactions)
	total := session.TotalUsage(snapshot)
	result.Usage = newDisplayUsage(total)
	result.ReasoningTokens = total.ReasoningTokens
	records, priced := 0, 0
	count := func(u llm.Usage) {
		records++
		if u.Cost != nil {
			priced++
		}
	}
	for _, entry := range snapshot.Messages {
		if a, ok := entry.Message.(llm.AssistantMessage); ok {
			count(a.Usage)
		}
	}
	for _, entry := range snapshot.Compactions {
		count(entry.Usage)
	}
	if priced > 0 {
		result.CostStatus = "Partial estimate"
		if priced == records {
			result.CostStatus = "Estimate"
		}
	}
	return result, nil
}
