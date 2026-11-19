package exa

import "encoding/json"

// searchRequest is the subset of the documented POST /search body this
// adapter sends. Only highlights are requested: no full text, no generated
// summary, no deep-search variants.
type searchRequest struct {
	Query          string          `json:"query"`
	Type           string          `json:"type"`
	NumResults     int             `json:"numResults"`
	IncludeDomains []string        `json:"includeDomains,omitempty"`
	ExcludeDomains []string        `json:"excludeDomains,omitempty"`
	Contents       requestContents `json:"contents"`
}

type requestContents struct {
	Text       bool `json:"text"`
	Highlights bool `json:"highlights"`
}

// searchResponse tolerates unknown fields; the presence of "results" is
// checked separately from an empty array.
type searchResponse struct {
	RequestID   string          `json:"requestId"`
	Results     *[]searchResult `json:"results"`
	CostDollars *costDollars    `json:"costDollars"`
	Error       json.RawMessage `json:"error"`
}

type searchResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate"`
	Text          string   `json:"text"`
	Highlights    []string `json:"highlights"`
	Summary       string   `json:"summary"`
}

type costDollars struct {
	Total *float64 `json:"total"`
}

// errorResponse is the documented error envelope: a human-readable "error"
// string and an open-ended machine tag.
type errorResponse struct {
	Error json.RawMessage `json:"error"`
	Tag   string          `json:"tag"`
}
