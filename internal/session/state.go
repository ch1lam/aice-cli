package session

type recordIndex struct {
	nodes       map[string]Node
	messages    map[string]MessageEntry
	compactions map[string]Compaction
	nodeTypes   map[string]RecordType
	parents     map[string]string
	recordIDs   map[string]struct{}
}

func indexRecords(
	messages []MessageEntry,
	compactions []Compaction,
	leaves []Leaf,
) recordIndex {
	index := recordIndex{
		nodes:       make(map[string]Node),
		messages:    make(map[string]MessageEntry),
		compactions: make(map[string]Compaction),
		nodeTypes:   make(map[string]RecordType),
		parents:     make(map[string]string),
		recordIDs:   make(map[string]struct{}),
	}
	for _, message := range messages {
		index.nodes[message.ID] = Node{
			Type:      message.Type,
			ID:        message.ID,
			ParentID:  message.ParentID,
			Timestamp: message.CreatedAt,
		}
		index.messages[message.ID] = message
		index.nodeTypes[message.ID] = message.Type
		index.parents[message.ID] = message.ParentID
		index.recordIDs[message.ID] = struct{}{}
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
	messages    []MessageEntry
	compactions []Compaction
	leafMoves   []Leaf
	order       []string
	index       recordIndex
	leafID      string
}

func newStoreState(snapshot Snapshot) storeState {
	return storeState{
		header:      snapshot.Header,
		messages:    snapshot.Messages,
		compactions: snapshot.Compactions,
		leafMoves:   snapshot.LeafMoves,
		order:       snapshot.Order,
		index:       indexRecords(snapshot.Messages, snapshot.Compactions, snapshot.LeafMoves),
		leafID:      snapshot.LeafID,
	}
}

func (state *storeState) retainMessage(message MessageEntry) {
	state.messages = append(state.messages, message)
	state.index.messages[message.ID] = message
	state.index.nodes[message.ID] = Node{Type: message.Type, ID: message.ID, ParentID: message.ParentID, Timestamp: message.CreatedAt}
	state.retain(message.ID, message.ParentID, message.Type)
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
