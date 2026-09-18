package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TitleRecord changes the display name of the entire session. It is metadata,
// not a conversation node; an empty title restores the derived default.
type TitleRecord struct {
	Type      RecordType `json:"type"`
	ID        string     `json:"id"`
	CreatedAt int64      `json:"created_at"`
	Title     string     `json:"title"`
}

// NewTitle normalizes whitespace and validates a display-title change.
func NewTitle(id, title string, createdAt int64) (TitleRecord, error) {
	record := TitleRecord{Type: RecordTypeTitle, ID: id, CreatedAt: createdAt, Title: strings.Join(strings.Fields(title), " ")}
	return record, record.Validate()
}

// Validate checks the record without interpreting its title as instructions.
func (t TitleRecord) Validate() error {
	if t.Type != RecordTypeTitle {
		return fmt.Errorf("session: invalid title record type %q", t.Type)
	}
	if err := validateRecordID("title", t.ID, false); err != nil {
		return err
	}
	if t.CreatedAt <= 0 {
		return fmt.Errorf("session: title creation time must be positive")
	}
	if !utf8.ValidString(t.Title) || utf8.RuneCountInString(t.Title) > 200 {
		return fmt.Errorf("session: title must be valid text of at most 200 characters")
	}
	for _, r := range t.Title {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("session: title contains a control character")
		}
	}
	return nil
}

// AppendTitle durably appends metadata without moving the active branch or
// entering the title into model context. It shares the Store's writer lock.
func (s *Store) AppendTitle(ctx context.Context, title TitleRecord) error {
	if s == nil {
		return fmt.Errorf("session: store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateWritable(ctx); err != nil {
		return err
	}
	if err := title.Validate(); err != nil {
		return err
	}
	if _, exists := s.index.recordIDs[title.ID]; exists {
		return fmt.Errorf("session: duplicate record id %q", title.ID)
	}
	data, err := json.Marshal(title)
	if err != nil {
		return fmt.Errorf("session: encode title: %w", err)
	}
	if err := s.appendData(ctx, "title", data); err != nil {
		return err
	}
	s.retainTitle(title)
	return nil
}

func (s *storeState) retainTitle(title TitleRecord) {
	s.titles = append(s.titles, title)
	s.index.recordIDs[title.ID] = struct{}{}
}
