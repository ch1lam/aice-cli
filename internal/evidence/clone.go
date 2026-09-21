package evidence

import "slices"

// Clone deep-copies a bundle so frozen Session snapshots and live displays never
// share mutable slices. Strings are immutable and shared.
func (b *Bundle) Clone() *Bundle {
	if b == nil {
		return nil
	}
	cloned := &Bundle{
		Sources:     slices.Clone(b.Sources),
		Items:       slices.Clone(b.Items),
		Diagnostics: b.Diagnostics,
	}
	cloned.Diagnostics.Warnings = slices.Clone(b.Diagnostics.Warnings)
	if b.Diagnostics.ReportedCost != nil {
		cost := *b.Diagnostics.ReportedCost
		cloned.Diagnostics.ReportedCost = &cost
	}
	return cloned
}

// Source returns the source with the given ID.
func (b *Bundle) Source(id string) (Source, bool) {
	if b == nil {
		return Source{}, false
	}
	for _, source := range b.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return Source{}, false
}
