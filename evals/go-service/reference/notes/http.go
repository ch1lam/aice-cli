package notes

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// NewHandler creates an independent in-memory notes service.
func NewHandler() http.Handler {
	s := &store{notes: make(map[int]note)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /notes", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Text string `json:"text"`
			Tag  string `json:"tag"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "expected one JSON object", http.StatusBadRequest)
			return
		}
		input.Text = strings.TrimSpace(input.Text)
		if input.Text == "" || utf8.RuneCountInString(input.Text) > 200 || !validTag(input.Tag) {
			http.Error(w, "invalid text or tag", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, s.add(input.Text, input.Tag))
	})
	mux.HandleFunc("GET /notes", func(w http.ResponseWriter, r *http.Request) {
		tag := r.URL.Query().Get("tag")
		if !validTag(tag) {
			http.Error(w, "invalid tag", http.StatusBadRequest)
			return
		}
		limit := 0
		if values, present := r.URL.Query()["limit"]; present {
			var err error
			limit, err = strconv.Atoi(values[0])
			if err != nil || limit < 1 || limit > 100 || len(values) != 1 {
				http.Error(w, "limit must be 1..100", http.StatusBadRequest)
				return
			}
		}
		writeJSON(w, http.StatusOK, s.list(tag, limit))
	})
	mux.HandleFunc("DELETE /notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id < 1 || !s.delete(id) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func validTag(tag string) bool {
	if len(tag) > 20 {
		return false
	}
	for _, c := range tag {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The response is already committed; a disconnected client needs no second response.
	_ = json.NewEncoder(w).Encode(value)
}
