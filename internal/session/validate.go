package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func validateNode(
	id string,
	parentID string,
	leafID string,
	recordIDs map[string]struct{},
	nodeTypes map[string]RecordType,
) error {
	if _, exists := recordIDs[id]; exists {
		return fmt.Errorf("session: duplicate record id %q", id)
	}
	if parentID != leafID {
		return fmt.Errorf(
			"session: node parent %q is not current leaf %q",
			parentID,
			leafID,
		)
	}
	if parentID != "" {
		if _, exists := nodeTypes[parentID]; !exists {
			return fmt.Errorf("%w: %s", ErrEntryNotFound, parentID)
		}
	}
	return nil
}

func validateLeaf(
	leaf Leaf,
	leafID string,
	recordIDs map[string]struct{},
	nodeTypes map[string]RecordType,
) error {
	if _, exists := recordIDs[leaf.ID]; exists {
		return fmt.Errorf("session: duplicate record id %q", leaf.ID)
	}
	if leaf.ParentID != leafID {
		return fmt.Errorf(
			"session: leaf parent %q is not current leaf %q",
			leaf.ParentID,
			leafID,
		)
	}
	if leaf.TargetID != "" {
		if _, exists := nodeTypes[leaf.TargetID]; !exists {
			return fmt.Errorf("%w: %s", ErrEntryNotFound, leaf.TargetID)
		}
	}
	return nil
}

func validateCompactionBoundary(
	compaction Compaction,
	index recordIndex,
) error {
	nodeTypes, parents, compactions := index.nodeTypes, index.parents, index.compactions
	if err := completeBoundary(index, compaction.ParentID); err != nil {
		return err
	}

	if err := compaction.Validate(); err != nil {
		return err
	}
	path, err := pathToRoot(compaction.ParentID, nodeTypes, parents)
	if err != nil {
		return err
	}
	activeMessageIDs, err := activeMessageIDs(path, nodeTypes, compactions)
	if err != nil {
		return err
	}
	if len(activeMessageIDs) != compaction.ActiveMessageCount {
		return fmt.Errorf(
			"session: compaction active message count is %d, current branch has %d",
			compaction.ActiveMessageCount,
			len(activeMessageIDs),
		)
	}
	if compaction.FirstKeptMessageID == "" {
		if compaction.RetainedMessageCount != 0 {
			return fmt.Errorf("session: full compaction must retain no source messages")
		}
		return nil
	}
	firstKept := -1
	for index, messageID := range activeMessageIDs {
		if messageID == compaction.FirstKeptMessageID {
			firstKept = index
			break
		}
	}
	if firstKept >= 0 {
		if err := completeBoundary(index, parents[compaction.FirstKeptMessageID]); err != nil {
			return fmt.Errorf("session: compaction cuts a tool group: %w", err)
		}
	}
	if firstKept <= 0 {
		return fmt.Errorf(
			"session: compaction first kept message %q does not leave older branch history",
			compaction.FirstKeptMessageID,
		)
	}
	if retained := len(activeMessageIDs) - firstKept; retained != compaction.RetainedMessageCount {
		return fmt.Errorf(
			"session: compaction retained message count is %d, branch boundary retains %d",
			compaction.RetainedMessageCount,
			retained,
		)
	}
	return nil
}

func pathToRoot(
	leafID string,
	nodeTypes map[string]RecordType,
	parents map[string]string,
) ([]string, error) {
	if leafID == "" {
		return nil, nil
	}
	path := make([]string, 0)
	seen := make(map[string]struct{})
	current := leafID
	for current != "" {
		if _, exists := seen[current]; exists {
			return nil, fmt.Errorf("session: cycle at entry %q", current)
		}
		seen[current] = struct{}{}
		if _, exists := nodeTypes[current]; !exists {
			return nil, fmt.Errorf("%w: %s", ErrEntryNotFound, current)
		}
		path = append(path, current)
		current = parents[current]
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path, nil
}

func activeMessageIDs(
	path []string,
	nodeTypes map[string]RecordType,
	compactions map[string]Compaction,
) ([]string, error) {
	latestCompaction := -1
	for index, id := range path {
		if nodeTypes[id] == RecordTypeCompaction {
			latestCompaction = index
		}
	}
	start := 0
	if latestCompaction >= 0 {
		compaction, exists := compactions[path[latestCompaction]]
		if !exists {
			return nil, fmt.Errorf(
				"session: compaction entry %q is missing",
				path[latestCompaction],
			)
		}
		if compaction.FirstKeptMessageID == "" {
			// The checkpoint summarizes every source message on the branch. Future
			// messages appended after the checkpoint remain active.
			start = latestCompaction
		} else {
			start = -1
			for index := 0; index < latestCompaction; index++ {
				if path[index] == compaction.FirstKeptMessageID {
					if nodeTypes[path[index]] != RecordTypeMessage {
						return nil, fmt.Errorf(
							"session: compaction first kept entry %q is not a message",
							compaction.FirstKeptMessageID,
						)
					}
					start = index
					break
				}
			}
			if start < 0 {
				return nil, fmt.Errorf(
					"session: compaction first kept message %q is not an ancestor",
					compaction.FirstKeptMessageID,
				)
			}
		}
	}
	messageIDs := make([]string, 0)
	for index, id := range path {
		if latestCompaction >= 0 && index < start {
			continue
		}
		if nodeTypes[id] == RecordTypeMessage {
			messageIDs = append(messageIDs, id)
		}
	}
	return messageIDs, nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("session: context is required")
	}
	return ctx.Err()
}

func validatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("session: file path is required")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("session: file path contains a null byte")
	}
	return nil
}

func validateHeader(header Header) error {
	if header.Type != RecordTypeSession {
		return fmt.Errorf("session: header has type %q", header.Type)
	}
	if header.Version != CurrentVersion {
		return fmt.Errorf(
			"%w: got %d, want %d",
			ErrUnsupportedVersion,
			header.Version,
			CurrentVersion,
		)
	}
	if strings.TrimSpace(header.ID) == "" {
		return fmt.Errorf("session: header id is required")
	}
	if strings.IndexByte(header.ID, 0) >= 0 {
		return fmt.Errorf("session: header id contains a null byte")
	}
	if header.CreatedAt <= 0 {
		return fmt.Errorf("session: header creation time must be positive")
	}
	if strings.TrimSpace(header.WorkingDirectory) == "" {
		return fmt.Errorf("session: header working directory is required")
	}
	if strings.IndexByte(header.WorkingDirectory, 0) >= 0 {
		return fmt.Errorf("session: header working directory contains a null byte")
	}
	if !filepath.IsAbs(header.WorkingDirectory) {
		return fmt.Errorf("session: header working directory must be absolute")
	}
	return nil
}
