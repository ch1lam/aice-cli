// Package httpfetch retrieves public web pages for the web_fetch tool with
// address validation, pinned dialing, bounded redirects and bounded bodies.
// These protections bound this one entry point; they are not a process-wide
// network sandbox.
package httpfetch

import (
	"net/netip"
	"net/url"

	"github.com/ch1lam/aice-cli/internal/web"
)

// target is one validated hop.
type target struct {
	url    *url.URL
	host   string // hostname without brackets, lowercase
	port   int
	origin string // scheme://host[:port]
}

// validateURL applies the shared URL policy and parses the normalized result.
func validateURL(raw string) (target, error) {
	validated, err := web.ValidateFetchURL(raw)
	if err != nil {
		return target{}, err
	}
	parsed, err := url.Parse(validated.URL)
	if err != nil {
		return target{}, web.NewError(web.CodeInvalidArgument, "parse url: %v", err)
	}
	return target{url: parsed, host: validated.Host, port: validated.Port, origin: validated.Origin}, nil
}

// OriginOf validates a raw URL and returns its permission origin.
func OriginOf(raw string) (string, error) {
	validated, err := web.ValidateFetchURL(raw)
	if err != nil {
		return "", err
	}
	return validated.Origin, nil
}

// literalAddr returns the IP when the host is an address literal.
func literalAddr(host string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr, true
}
