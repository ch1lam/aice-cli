package session

type recordIndex struct {
	nodes       map[string]Node
	turns       map[string]Turn
	compactions map[string]Compaction
	nodeTypes   map[string]RecordType
	parents     map[string]string
	recordIDs   map[string]struct{}
}

func indexRecords(
	turns []Turn,
	compactions []Compaction,
	leaves []Leaf,
) recordIndex {
	index := recordIndex{
		nodes:       make(map[string]Node),
		turns:       make(map[string]Turn),
		compactions: make(map[string]Compaction),
		nodeTypes:   make(map[string]RecordType),
		parents:     make(map[string]string),
		recordIDs:   make(map[string]struct{}),
	}
	for _, turn := range turns {
		index.nodes[turn.ID] = Node{
			Type:      turn.Type,
			ID:        turn.ID,
			ParentID:  turn.ParentID,
			Timestamp: turn.CompletedAt,
		}
		index.turns[turn.ID] = turn
		index.nodeTypes[turn.ID] = turn.Type
		index.parents[turn.ID] = turn.ParentID
		index.recordIDs[turn.ID] = struct{}{}
	}
	for _, compaction := range compactions {
		index.nodes[compaction.ID] = Node{
			Type:      compaction.Type,
			ID:        compaction.ID,
			ParentID:  compaction.ParentID,
			Timestamp: compaction.CreatedAt,
		}
		index.compactions[compaction.ID] = compaction
		index.nodeTypes[compaction.ID] = compaction.Type
		index.parents[compaction.ID] = compaction.ParentID
		index.recordIDs[compaction.ID] = struct{}{}
	}
	for _, leaf := range leaves {
		index.recordIDs[leaf.ID] = struct{}{}
	}
	return index
}

type storeState struct {
	header      Header
	turns       []Turn
	compactions []Compaction
	leafMoves   []Leaf
	order       []string
	index       recordIndex
	leafID      string
}

func newStoreState(snapshot Snapshot) storeState {
	return storeState{
		header:      snapshot.Header,
		turns:       snapshot.Turns,
		compactions: snapshot.Compactions,
		leafMoves:   snapshot.LeafMoves,
		order:       snapshot.Order,
		index:       indexRecords(snapshot.Turns, snapshot.Compactions, snapshot.LeafMoves),
		leafID:      snapshot.LeafID,
	}
}

func (state *storeState) retainTurn(turn Turn) {
	state.turns = append(state.turns, turn)
	state.retain(turn.ID, turn.ParentID, turn.Type)
}

func (state *storeState) retainCompaction(compaction Compaction) {
	state.compactions = append(state.compactions, compaction)
	state.index.compactions[compaction.ID] = compaction
	state.retain(compaction.ID, compaction.ParentID, compaction.Type)
}

func (state *storeState) retainLeaf(leaf Leaf) {
	state.leafMoves = append(state.leafMoves, leaf)
	state.index.recordIDs[leaf.ID] = struct{}{}
	state.leafID = leaf.TargetID
}

func (state *storeState) retain(id string, parentID string, recordType RecordType) {
	state.index.nodeTypes[id] = recordType
	state.index.parents[id] = parentID
	state.index.recordIDs[id] = struct{}{}
	state.order = append(state.order, id)
	state.leafID = id
}
