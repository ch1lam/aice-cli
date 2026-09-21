package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeURLSafeCanonicalizationOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"scheme host case and default port", "HTTPS://Example.COM:443/Docs?b=2&a=1#Frag", "https://example.com/Docs?b=2&a=1#Frag", false},
		{"http default port", "http://example.com:80/x", "http://example.com/x", false},
		{"non default port kept", "http://example.com:8080/x", "http://example.com:8080/x", false},
		{"query order kept", "https://example.com/?z=1&a=2", "https://example.com/?z=1&a=2", false},
		{"userinfo rejected", "https://user:pw@example.com/", "", true},
		{"ftp rejected", "ftp://example.com/", "", true},
		{"relative rejected", "/docs", "", true},
		{"whitespace rejected", "https://exa mple.com", "", true},
		{"idn host and escaped path", "https://例え.jp/路径", "https://xn--r8jz45g.jp/%E8%B7%AF%E5%BE%84", false},
		{"ipv6 literal kept", "http://[::1]:8080/x", "http://[::1]:8080/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeURL(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSourceIDIsDeterministic(t *testing.T) {
	t.Parallel()
	a, err := NewSource("HTTPS://Example.com/a", "T", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSource("https://example.com/a", "Other", "2023-11-16T01:36:32.547Z")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || len(a.ID) != 32 {
		t.Fatalf("ids differ or malformed: %q %q", a.ID, b.ID)
	}
	if b.PublishedAt != "2023-11-16T01:36:32Z" {
		t.Fatalf("published = %q", b.PublishedAt)
	}
	c, _ := NewSource("https://example.com/a", "", "not a date")
	if c.PublishedAt != "" {
		t.Fatalf("invalid dates must stay absent, got %q", c.PublishedAt)
	}
	if d, _ := NewSource("https://example.com/a", "", "2024-02-03"); d.PublishedAt != "2024-02-03T00:00:00Z" {
		t.Fatalf("plain date = %q", d.PublishedAt)
	}
}

func TestBundleValidateAndClone(t *testing.T) {
	t.Parallel()
	source, _ := NewSource("https://example.com/a", "A", "")
	bundle := &Bundle{
		Sources: []Source{source},
		Items: []Evidence{{
			SourceID: source.ID, Kind: KindExcerpt, Text: "hello", Format: FormatText,
			Acquisition: AcquisitionSearchService, RetrievedAt: 1, ReturnedBytes: 5,
		}},
		Diagnostics: Diagnostics{Warnings: []string{"w"}, ReportedCost: &Cost{Amount: 0.1, Currency: "USD", Source: "upstream"}},
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
	clone := bundle.Clone()
	clone.Sources[0].Title = "changed"
	clone.Items[0].Text = "changed"
	clone.Diagnostics.Warnings[0] = "changed"
	clone.Diagnostics.ReportedCost.Amount = 9
	if bundle.Sources[0].Title != "A" || bundle.Items[0].Text != "hello" || bundle.Diagnostics.Warnings[0] != "w" || bundle.Diagnostics.ReportedCost.Amount != 0.1 {
		t.Fatal("clone shares mutable state with original")
	}
	var nilBundle *Bundle
	if nilBundle.Clone() != nil || nilBundle.Validate() != nil {
		t.Fatal("nil bundle must clone and validate as nil")
	}

	bad := bundle.Clone()
	bad.Items[0].SourceID = "missing"
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Fatalf("dangling source id accepted: %v", err)
	}
	bad = bundle.Clone()
	bad.Items[0].Kind = "guess"
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown kind accepted")
	}
	bad = bundle.Clone()
	bad.Sources[0].URL = "https://Example.com/a"
	if err := bad.Validate(); err == nil {
		t.Fatal("non-normalized url accepted")
	}
	bad = bundle.Clone()
	bad.Sources = append(bad.Sources, source)
	if err := bad.Validate(); err == nil {
		t.Fatal("duplicate source accepted")
	}
	bad = bundle.Clone()
	bad.Items[0].Text = strings.Repeat("x", MaxBundleBytes)
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized bundle accepted: %v", err)
	}
	bad = bundle.Clone()
	bad.Diagnostics.ReportedCost = &Cost{Amount: 0}
	if err := bad.Validate(); err == nil {
		t.Fatal("cost without currency accepted")
	}
}

func TestBundleJSONRoundTripOmitsUnknownValues(t *testing.T) {
	t.Parallel()
	source, _ := NewSource("https://example.com/a", "", "")
	bundle := &Bundle{Sources: []Source{source}, Items: []Evidence{{SourceID: source.ID, Kind: KindDocument, Text: "d", Format: FormatMarkdown, Acquisition: AcquisitionHTTPFetch, RetrievedAt: 7}}}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "reported_cost") || strings.Contains(string(data), "published_at") {
		t.Fatalf("unknown values must stay absent: %s", data)
	}
	var decoded Bundle
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	if decoded.Items[0].Text != "d" || decoded.Sources[0].ID != source.ID {
		t.Fatalf("round trip lost data: %+v", decoded)
	}
}
