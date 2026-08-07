package session

import "fmt"

type snapshotIndex struct {
	nodes       map[string]Node
	turns       map[string]Turn
	compactions map[string]Compaction
	nodeTypes   map[string]RecordType
	parents     map[string]string
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
	index := indexRecords(snapshot.Turns, snapshot.Compactions, nil)
	seen := make(map[string]struct{}, len(snapshot.Turns)+len(snapshot.Compactions))
	for _, turn := range snapshot.Turns {
		if err := turn.Validate(); err != nil {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot turn %q: %w",
				turn.ID,
				err,
			)
		}
		if _, exists := seen[turn.ID]; exists {
			return snapshotIndex{}, fmt.Errorf(
				"session: duplicate tree node id %q",
				turn.ID,
			)
		}
		seen[turn.ID] = struct{}{}
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
			index.nodeTypes,
			index.parents,
			index.compactions,
		); err != nil {
			return snapshotIndex{}, fmt.Errorf(
				"session: snapshot compaction %q boundary: %w",
				compaction.ID,
				err,
			)
		}
	}
	return snapshotIndex{
		nodes:       index.nodes,
		turns:       index.turns,
		compactions: index.compactions,
		nodeTypes:   index.nodeTypes,
		parents:     index.parents,
	}, nil
}
