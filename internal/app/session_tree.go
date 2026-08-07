package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// SessionTree writes an append-order-stable view of all branches.
func (a *application) SessionTree(
	ctx context.Context,
	request cli.SessionTreeRequest,
	output io.Writer,
) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if output == nil {
		return fmt.Errorf("app: output is required")
	}
	workspace, err := tool.NewWorkspace(request.Workspace)
	if err != nil {
		return fmt.Errorf("app: create workspace: %w", err)
	}
	store, snapshot, err := openExistingSession(
		ctx,
		workspace,
		request.Session,
	)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, store.Close())
	}()

	return writeSessionTree(output, snapshot)
}

func writeSessionTree(
	output io.Writer,
	snapshot session.Snapshot,
) error {
	nodes, err := session.Nodes(snapshot)
	if err != nil {
		return fmt.Errorf("app: read session tree: %w", err)
	}
	activeBranch, err := session.ActiveBranch(snapshot)
	if err != nil {
		return fmt.Errorf("app: read active session branch: %w", err)
	}
	active := make(map[string]struct{}, len(activeBranch))
	for _, node := range activeBranch {
		active[node.ID] = struct{}{}
	}
	children := make(map[string][]session.Node)
	for _, node := range nodes {
		children[node.ParentID] = append(children[node.ParentID], node)
	}
	if _, err := fmt.Fprintf(
		output,
		"Session %s (%d node(s))\n",
		snapshot.Header.ID,
		len(nodes),
	); err != nil {
		return fmt.Errorf("app: write session tree header: %w", err)
	}
	rootMarker := "-"
	if snapshot.LeafID == "" {
		rootMarker = "*"
	}
	if _, err := fmt.Fprintf(output, "%s root\n", rootMarker); err != nil {
		return fmt.Errorf("app: write session root: %w", err)
	}
	turns := make(map[string]session.Turn, len(snapshot.Turns))
	for _, turn := range snapshot.Turns {
		turns[turn.ID] = turn
	}
	compactions := make(
		map[string]session.Compaction,
		len(snapshot.Compactions),
	)
	for _, compaction := range snapshot.Compactions {
		compactions[compaction.ID] = compaction
	}
	if err := writeSessionChildren(
		output,
		children,
		turns,
		compactions,
		active,
		snapshot.LeafID,
		"",
		1,
	); err != nil {
		return err
	}
	return nil
}

// CheckoutSession appends a leaf move; the next turn will become a child of
// the selected node and therefore create a branch.
func (a *application) CheckoutSession(
	ctx context.Context,
	request cli.SessionCheckoutRequest,
	output io.Writer,
) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("app: context is required")
	}
	if output == nil {
		return fmt.Errorf("app: output is required")
	}
	workspace, err := tool.NewWorkspace(request.Workspace)
	if err != nil {
		return fmt.Errorf("app: create workspace: %w", err)
	}
	store, _, err := openExistingSession(
		ctx,
		workspace,
		request.Session,
	)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, store.Close())
	}()

	_, err = checkoutSessionStore(ctx, store, request.Entry, output)
	return err
}

func checkoutSessionStore(
	ctx context.Context,
	store *session.Store,
	entry string,
	output io.Writer,
) (bool, error) {
	if store == nil {
		return false, fmt.Errorf("app: session store is required")
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		return false, fmt.Errorf("app: read session: %w", err)
	}
	targetID := strings.TrimSpace(entry)
	if targetID == "root" {
		targetID = ""
	} else {
		if _, err := session.Branch(snapshot, targetID); err != nil {
			return false, fmt.Errorf("app: find session entry %q: %w", targetID, err)
		}
	}
	if targetID == snapshot.LeafID {
		if _, err := fmt.Fprintf(
			output,
			"Session is already at %s.\n",
			sessionEntryName(targetID),
		); err != nil {
			return false, fmt.Errorf("app: write checkout result: %w", err)
		}
		return false, nil
	}
	leafID, err := session.NewID()
	if err != nil {
		return false, fmt.Errorf("app: generate leaf record id: %w", err)
	}
	leaf, err := session.NewLeaf(
		leafID,
		snapshot.LeafID,
		targetID,
		time.Now().UnixMilli(),
	)
	if err != nil {
		return false, fmt.Errorf("app: create leaf record: %w", err)
	}
	if err := store.AppendLeaf(ctx, leaf); err != nil {
		return false, fmt.Errorf("app: append leaf record: %w", err)
	}
	if _, err := fmt.Fprintf(
		output,
		"Checked out %s. The next turn will branch from this point.\n",
		sessionEntryName(targetID),
	); err != nil {
		return false, fmt.Errorf("app: write checkout result: %w", err)
	}
	return true, nil
}

func writeSessionChildren(
	output io.Writer,
	children map[string][]session.Node,
	turns map[string]session.Turn,
	compactions map[string]session.Compaction,
	active map[string]struct{},
	leafID string,
	parentID string,
	depth int,
) error {
	for _, node := range children[parentID] {
		marker := "-"
		if node.ID == leafID {
			marker = "*"
		} else if _, exists := active[node.ID]; exists {
			marker = "+"
		}
		if _, err := fmt.Fprintf(
			output,
			"%s%s %s %s %s\n",
			strings.Repeat("  ", depth),
			marker,
			node.Type,
			node.ID,
			sessionNodeDescription(node, turns, compactions),
		); err != nil {
			return fmt.Errorf("app: write session tree node: %w", err)
		}
		if err := writeSessionChildren(
			output,
			children,
			turns,
			compactions,
			active,
			leafID,
			node.ID,
			depth+1,
		); err != nil {
			return err
		}
	}
	return nil
}

func sessionNodeDescription(
	node session.Node,
	turns map[string]session.Turn,
	compactions map[string]session.Compaction,
) string {
	switch node.Type {
	case session.RecordTypeTurn:
		turn, exists := turns[node.ID]
		if !exists || len(turn.Messages) == 0 {
			return ""
		}
		user, ok := turn.Messages[0].(llm.UserMessage)
		if !ok {
			return ""
		}
		return quoteSessionText(visibleText(user.Content, "\n"))
	case session.RecordTypeCompaction:
		compaction, exists := compactions[node.ID]
		if !exists {
			return ""
		}
		return quoteSessionText(compaction.Summary)
	default:
		return ""
	}
}

func quoteSessionText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const limit = 72
	runes := []rune(text)
	if len(runes) > limit {
		text = string(runes[:limit-3]) + "..."
	}
	if text == "" {
		return ""
	}
	return fmt.Sprintf("%q", text)
}

// visibleText joins the non-blank text parts in content with separator.
func visibleText(content []llm.ContentPart, separator string) string {
	parts := make([]string, 0, len(content))
	for _, part := range content {
		if part.Type != llm.ContentTypeText {
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, separator)
}

func sessionEntryName(id string) string {
	if id == "" {
		return "Session root"
	}
	return fmt.Sprintf("Session entry %s", id)
}
