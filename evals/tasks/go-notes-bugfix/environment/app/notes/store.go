package notes

import (
	"sort"
	"sync"
)

type note struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	Tag  string `json:"tag"`
}

// store owns notes and ID allocation. Returned values cannot mutate its state.
type store struct {
	mu     sync.Mutex
	nextID int
	notes  map[int]note
}

func (s *store) add(text, tag string) note {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	n := note{ID: s.nextID, Text: text, Tag: tag}
	s.notes[n.ID] = n
	return n
}

func (s *store) list(tag string, limit int) []note {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]note, 0)
	for _, n := range s.notes {
		if tag == "" || n.Tag == tag {
			items = append(items, n)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func (s *store) delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.notes[id]; !ok {
		return false
	}
	delete(s.notes, id)
	return true
}
