package app

import (
	"context"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
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
	s.stateMu.Lock()
	throughput := s.throughput
	s.stateMu.Unlock()
	revision, _ := s.settingsStatus()
	result := interaction.UsageSnapshot{Revision: revision, ReadAt: time.Now(), Directory: s.workspacePath,
		Provider: string(settings.model.Provider), Model: settings.model.ID,
		Context:      s.contextSnapshotFor(settings.model, settings.configuration, settings.systemPrompt),
		WindowSource: contextWindowInformation(settings.model, settings.configuration), CostStatus: "Unavailable",
		Scope: "Recorded usage across all branches, compactions and reported subagent runs; excludes temporary BTW answers. Interrupted requests may be unreported.", Throughput: throughput}
	// The preparation reservation excludes a Session switch, so the current
	// Store stays open without holding the commit lock.
	store := s.conversation.currentStore()
	if store == nil {
		return result, nil
	}
	info, err := store.Info()
	if err != nil {
		return result, err
	}
	summary, err := store.Usage()
	if err != nil {
		return result, err
	}
	if result.LeafID, err = store.LeafID(); err != nil {
		return result, err
	}
	result.SessionID, result.Path = info.Header.ID, store.Path()
	result.Directory, result.CreatedAt = info.Header.WorkingDirectory, info.Header.CreatedAt
	result.Nodes, result.Messages, result.Compactions = summary.Messages+summary.Compactions, summary.Messages, summary.Compactions
	result.Usage = newDisplayUsage(summary.Total)
	result.ReasoningTokens = summary.Total.ReasoningTokens
	if llm.ContextTokens(summary.LastCall) > 0 {
		result.LastCall = newDisplayUsage(summary.LastCall)
	}
	result.Reviews = summary.Reviews
	result.ReviewUsage = newDisplayUsage(summary.ReviewUsage)
	result.Delegations = summary.Delegations
	result.DelegatedUsage = newDisplayUsage(summary.DelegatedUsage)
	if summary.Priced > 0 {
		result.CostStatus = "Partial estimate"
		if summary.Priced == summary.Records {
			result.CostStatus = "Estimate"
		}
	}
	return result, nil
}
