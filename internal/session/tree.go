package session

import "fmt"

type snapshotIndex struct {
	nodes       map[string]Node
	turns       map[string]Turn
	compactions map[string]Compaction
}

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
	path, err := pathToRoot(leafID, nodeTypes(index.nodes), parentIDs(index.nodes))
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
	index := snapshotIndex{
		nodes:       make(map[string]Node),
		turns:       make(map[string]Turn),
		compactions: make(map[string]Compaction),
	}
	for _, turn := range snapshot.Turns {
		if err := turn.Validate(); err != nil {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot turn %q: %w",
				turn.ID,
				err,
			)
		}
		if _, exists := index.nodes[turn.ID]; exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: duplicate tree node id %q",
				turn.ID,
			)
		}
		index.nodes[turn.ID] = Node{
			Type:      turn.Type,
			ID:        turn.ID,
			ParentID:  turn.ParentID,
			Timestamp: turn.CompletedAt,
		}
		index.turns[turn.ID] = turn
	}
	for _, compaction := range snapshot.Compactions {
		if err := compaction.Validate(); err != nil {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot compaction %q: %w",
				compaction.ID,
				err,
			)
		}
		if _, exists := index.nodes[compaction.ID]; exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: duplicate tree node id %q",
				compaction.ID,
			)
		}
		index.nodes[compaction.ID] = Node{
			Type:      compaction.Type,
			ID:        compaction.ID,
			ParentID:  compaction.ParentID,
			Timestamp: compaction.CreatedAt,
		}
		index.compactions[compaction.ID] = compaction
	}
	if len(snapshot.Order) != len(index.nodes) {
		return snapshotIndex{}, fmt.Errorf(
			"session: snapshot order has %d ids for %d nodes",
			len(snapshot.Order),
			len(index.nodes),
		)
	}
	seen := make(map[string]struct{}, len(snapshot.Order))
	for position, id := range snapshot.Order {
		node, exists := index.nodes[id]
		if !exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot order references missing node %q",
				id,
			)
		}
		if _, exists := seen[id]; exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot order repeats node %q",
				id,
			)
		}
		if node.ParentID != "" {
			if _, exists := seen[node.ParentID]; !exists {
				return snapshotIndex{}, fmt.Errorf(
					"session: snapshot node %q at position %d precedes parent %q",
					id,
					position,
					node.ParentID,
				)
			}
		}
		seen[id] = struct{}{}
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
	types := nodeTypes(index.nodes)
	parents := parentIDs(index.nodes)
	for _, compaction := range snapshot.Compactions {
		if err := validateCompactionBoundary(
			compaction,
			types,
			parents,
			index.compactions,
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

func nodeTypes(nodes map[string]Node) map[string]RecordType {
	types := make(map[string]RecordType, len(nodes))
	for id, node := range nodes {
		types[id] = node.Type
	}
	return types
}

func parentIDs(nodes map[string]Node) map[string]string {
	parents := make(map[string]string, len(nodes))
	for id, node := range nodes {
		parents[id] = node.ParentID
	}
	return parents
}
