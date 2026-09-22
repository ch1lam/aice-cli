// Package httpfetch retrieves public web pages for the web_fetch tool with
// URL policy, lightweight IP-literal checks, bounded redirects and bounded
// bodies. DNS, proxy selection and dialing are left to the standard HTTP
// transport. These protections bound this one entry point; they are not a
// process-wide network sandbox.
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

// OriginOf validates a raw URL and returns its origin.
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

// checkFetchTarget applies the lightweight fetch-URL check: an explicit
// localhost name or a blocked IP literal (loopback, private, link-local,
// CGNAT, metadata, multicast and the other special-purpose ranges) is
// refused before any request. Hostnames that are not literals are left to
// the transport and the proxy side to resolve; no DNS pre-resolution happens
// here.
func checkFetchTarget(hop target) error {
	if hop.host == "localhost" {
		return web.NewError(web.CodeBlockedTarget, "host localhost is not allowed")
	}
	if addr, ok := literalAddr(hop.host); ok {
		if err := checkPublicAddress(addr); err != nil {
			return err
		}
	}
	return nil
}

// blockedPrefixes are special-purpose ranges that web_fetch never requests
// by IP literal. IPv4-mapped IPv6 addresses are unmapped before the check.
var blockedPrefixes = func() []netip.Prefix {
	prefixes := []string{
		"0.0.0.0/8",       // this network
		"10.0.0.0/8",      // private
		"100.64.0.0/10",   // shared address space (CGNAT)
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local, including cloud metadata endpoints
		"172.16.0.0/12",   // private
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"192.88.99.0/24",  // 6to4 relay anycast
		"192.168.0.0/16",  // private
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved and broadcast
		"::/128",          // unspecified
		"::1/128",         // loopback
		"::ffff:0:0/96",   // IPv4-mapped (checked after unmapping as well)
		"64:ff9b::/96",    // NAT64 well-known prefix
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard-only
		"2001::/32",       // TEREDO
		"2001:2::/48",     // benchmarking
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4
		"fc00::/7",        // unique local
		"fe80::/10",       // link-local
		"fec0::/10",       // deprecated site-local
		"ff00::/8",        // multicast
	}
	parsed := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		parsed = append(parsed, netip.MustParsePrefix(prefix))
	}
	return parsed
}()

// checkPublicAddress rejects every non-global address. It does not rely on
// IsPrivate alone: loopback, link-local, multicast, unspecified, CGNAT and
// documentation ranges are all refused.
func checkPublicAddress(addr netip.Addr) error {
	if !addr.IsValid() {
		return web.NewError(web.CodeBlockedTarget, "invalid address")
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return web.NewError(web.CodeBlockedTarget, "address %s is not a public address", addr)
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return web.NewError(web.CodeBlockedTarget, "address %s is in a blocked range (%s)", addr, prefix)
		}
	}
	return nil
}
