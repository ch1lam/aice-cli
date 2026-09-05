package session

import (
	"fmt"
	"slices"
)

type snapshotIndex = recordIndex

// Nodes returns all conversation-tree nodes in physical append order.
func Nodes(snapshot Snapshot) ([]Node, error) {
	index, err := indexSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(snapshot.Order))
	for _, id := range snapshot.Order {
		nodes = append(nodes, index.nodes[id])
	}
	return nodes, nil
}

// ActiveBranch returns the root-to-leaf path selected by the latest record.
func ActiveBranch(snapshot Snapshot) ([]Node, error) {
	return Branch(snapshot, snapshot.LeafID)
}

// Branch returns the root-to-node path for one tree node. An empty leaf ID
// selects the tree root.
func Branch(snapshot Snapshot, leafID string) ([]Node, error) {
	index, err := indexSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	path, err := pathToRoot(leafID, index.nodeTypes, index.parents)
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, len(path))
	for position, id := range path {
		nodes[position] = index.nodes[id]
	}
	return nodes, nil
}

func indexSnapshot(snapshot Snapshot) (snapshotIndex, error) {
	index := indexRecords(snapshot.Messages, snapshot.Compactions, nil)
	seen := make(map[string]struct{}, len(snapshot.Messages)+len(snapshot.Compactions))
	for _, message := range snapshot.Messages {
		if err := message.Validate(); err != nil {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot message %q: %w",
				message.ID,
				err,
			)
		}
		if _, exists := seen[message.ID]; exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: duplicate tree node id %q",
				message.ID,
			)
		}
		seen[message.ID] = struct{}{}
	}
	for _, compaction := range snapshot.Compactions {
		if err := compaction.Validate(); err != nil {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot compaction %q: %w",
				compaction.ID,
				err,
			)
		}
		if _, exists := seen[compaction.ID]; exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: duplicate tree node id %q",
				compaction.ID,
			)
		}
		seen[compaction.ID] = struct{}{}
	}
	if len(snapshot.Order) != len(index.nodes) {
		return snapshotIndex{}, fmt.Errorf(
			"session: snapshot order has %d ids for %d nodes",
			len(snapshot.Order),
			len(index.nodes),
		)
	}
	orderSeen := make(map[string]struct{}, len(snapshot.Order))
	sequences := make(map[string]messageSequence, len(snapshot.Order))
	for position, id := range snapshot.Order {
		node, exists := index.nodes[id]
		if !exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot order references missing node %q",
				id,
			)
		}
		if _, exists := orderSeen[id]; exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot order repeats node %q",
				id,
			)
		}
		if node.ParentID != "" {
			if _, exists := orderSeen[node.ParentID]; !exists {
				return snapshotIndex{}, fmt.Errorf(
					"session: snapshot node %q at position %d precedes parent %q",
					id,
					position,
					node.ParentID,
				)
			}
		}
		sequence := sequences[node.ParentID]
		// Results remove pending calls. Copy the small group so sibling branches
		// and earlier prefixes retain their own validation state.
		sequence.pending = slices.Clone(sequence.pending)
		if node.Type == RecordTypeMessage {
			if err := sequence.accept(index.messages[id].Message); err != nil {
				return snapshotIndex{}, err
			}
		} else if len(sequence.pending) != 0 {
			return snapshotIndex{}, ErrIncompleteGroup
		}
		sequences[id] = sequence
		orderSeen[id] = struct{}{}
	}
	if snapshot.LeafID != "" {
		if _, exists := index.nodes[snapshot.LeafID]; !exists {
			return snapshotIndex{}, fmt.Errorf(
				"%w: %s",
				ErrEntryNotFound,
				snapshot.LeafID,
			)
		}
	}
	for _, compaction := range snapshot.Compactions {
		if err := validateCompactionBoundary(
			compaction,
			index,
		); err != nil {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot compaction %q boundary: %w",
				compaction.ID,
				err,
			)
		}
	}
	return index, nil
}
