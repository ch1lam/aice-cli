package httpfetch

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"github.com/ch1lam/aice-cli/internal/web"
)

// blockedPrefixes are special-purpose ranges that web_fetch never connects to.
// IPv4-mapped IPv6 addresses are unmapped before the check.
var blockedPrefixes = func() []netip.Prefix {
	prefixes := []string{
		"0.0.0.0/8",       // this network
		"10.0.0.0/8",      // private
		"100.64.0.0/10",   // shared address space (CGNAT)
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local
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

// Resolver looks up the addresses for a hostname.
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

// Dialer opens one TCP connection to a validated address.
type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

func defaultResolver(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// resolvePublic resolves a hop to one validated address. When any returned
// address is blocked the whole answer is refused: a mixed answer is treated as
// an attempt to smuggle a private target past the check.
func resolvePublic(ctx context.Context, resolver Resolver, hop target) (netip.AddrPort, error) {
	if addr, ok := literalAddr(hop.host); ok {
		if err := checkPublicAddress(addr); err != nil {
			return netip.AddrPort{}, err
		}
		return netip.AddrPortFrom(addr.Unmap(), uint16(hop.port)), nil
	}
	addrs, err := resolver(ctx, hop.host)
	if err != nil {
		return netip.AddrPort{}, web.WrapContextError(err, web.CodeUnavailable, fmt.Sprintf("resolve %s failed", hop.host))
	}
	if len(addrs) == 0 {
		return netip.AddrPort{}, web.NewError(web.CodeUnavailable, "resolve %s returned no addresses", hop.host)
	}
	for _, addr := range addrs {
		if err := checkPublicAddress(addr); err != nil {
			return netip.AddrPort{}, web.NewError(web.CodeBlockedTarget, "%s resolves to a blocked address: %v", hop.host, err.(*web.Error).Message)
		}
	}
	return netip.AddrPortFrom(addrs[0].Unmap(), uint16(hop.port)), nil
}

// pinnedDial ignores the transport-supplied address and connects to the
// already validated one, closing the DNS rebinding window between check and
// connect. TLS still verifies the original hostname because only DialContext
// is replaced.
func pinnedDial(dialer Dialer, pinned netip.AddrPort) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("httpfetch: unsupported network %q", network)
		}
		return dialer(ctx, "tcp", pinned.String())
	}
}
