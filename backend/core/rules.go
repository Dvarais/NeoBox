package core

import (
	"net/netip"
	"strings"
)

// Turning an observed connection into a routing rule.
//
// The Connections view lets the user click a live connection and route it
// direct, through the proxy or into the void. That click has to become a
// CustomRule, and the value it carries is not free-form: custom rules are
// emitted before ip_is_private, before the bypass-RU rule sets, before the
// FakeIP route and before the whitelist catch-all (see GenerateConfig), and
// sing-box takes the first rule that matches. A careless suggestion therefore
// does not merely fail — it preempts the rules the tunnel depends on, and the
// user has no way to see why their machine stopped talking to its own router.
//
// So the suggestion is made here, in one place, with tests, rather than in the
// renderer where nothing would catch it.

// fakeIPRange mirrors the inet4_range given to the fakeip DNS server in
// GenerateConfig. Addresses inside it are not real destinations: they are
// handed out by FakeDNS and translated back on the way out, which is why the
// route sending them to the proxy is placed after every exclusion. A custom
// rule would jump ahead of it and break FakeDNS wholesale.
var fakeIPRange = netip.MustParsePrefix("198.18.0.0/15")

// SuggestRuleTarget derives the Type and Value of a CustomRule from one
// observed connection. host is the sniffed destination hostname, which is
// frequently empty — plain TCP to a literal IP and some QUIC never yield one —
// and destinationIP is the address the connection actually went to.
//
// It reports ok=false when no rule can be written that would be both useful and
// safe; the caller is expected to offer no rule buttons at all for such a row.
func SuggestRuleTarget(host, destinationIP string) (ruleType, ruleValue string, ok bool) {
	if h := normaliseHost(host); h != "" {
		// domain_suffix rather than domain: it is what the rule-type dropdown
		// already defaults to, and one click on "api.example.com" covering its
		// subdomains is what a user reaching for this expects.
		return "domain_suffix", h, true
	}

	addr, err := netip.ParseAddr(strings.TrimSpace(destinationIP))
	if err != nil {
		return "", "", false
	}
	addr = addr.Unmap()
	if !isRoutableDestination(addr) {
		return "", "", false
	}
	if addr.Is4() {
		return "ip_cidr", addr.String() + "/32", true
	}
	return "ip_cidr", addr.String() + "/128", true
}

// NormaliseProcessName cleans an executable name for use as the Value of a
// CustomRule of type "process", reporting false when what is left could not
// match anything.
//
// The name arrives from the Connections view, where service.processName has
// already reduced the process path to its base name. It is still checked here:
// a rule carrying a separator or a wildcard would be written into
// settings.json, survive a restart, and silently never fire — and the user
// would have no way to see why.
//
// The case is deliberately left as it came. sing-box compares process_name
// literally, the name the view shows is the one Windows reported, and
// lowercasing it here would turn a rule that matches into one that does not.
// It is the same choice the split-tunnelling list already makes with the names
// the user types by hand.
func NormaliseProcessName(process string) (string, bool) {
	name := strings.TrimSpace(process)
	if name == "" {
		return "", false
	}
	// A value carrying a directory could never equal a base name.
	if strings.ContainsAny(name, `/\`) {
		return "", false
	}
	// process_name takes no globbing: sing-box compares the string as given, so
	// a wildcard is a rule that cannot fire. The rest are characters Windows
	// does not permit in a file name, i.e. evidence the value is not a name.
	if strings.ContainsAny(name, "*?\"<>|:") {
		return "", false
	}
	return name, true
}

// normaliseHost cleans up a sniffed hostname and returns "" if what is left
// cannot be used as a domain rule.
func normaliseHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	// A sniffed FQDN can carry the root label's trailing dot. It is the same
	// name either way, but "example.com." would never match a rule written as
	// "example.com".
	h = strings.Trim(h, ".")
	if h == "" {
		return ""
	}
	// Anything that is not a bare hostname is refused rather than guessed at:
	// a path, a port, an address in brackets, whitespace. These do not appear
	// in a well-behaved sniff result, and turning one into a rule would produce
	// an entry that silently never matches.
	if strings.ContainsAny(h, " \t\r\n/\\:[]@?#") {
		return ""
	}
	// A single label is not a routable domain — "localhost", a NetBIOS name, a
	// stray "*". Requiring a dot also throws out most malformed sniffs for free.
	if !strings.Contains(h, ".") {
		return ""
	}
	// A dotted string that parses as an address is an IP literal, not a domain,
	// and belongs on the ip_cidr path.
	if _, err := netip.ParseAddr(h); err == nil {
		return ""
	}
	return h
}

// isRoutableDestination reports whether addr is somewhere a user-defined rule
// may legitimately point. Everything it rejects is already handled by a rule
// that GenerateConfig places *after* the custom rules, so writing a custom rule
// for one of these addresses would override routing the tunnel relies on.
func isRoutableDestination(addr netip.Addr) bool {
	switch {
	case !addr.IsValid():
		return false
	case addr.IsUnspecified(), addr.IsLoopback(), addr.IsMulticast(),
		addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast(),
		addr.IsInterfaceLocalMulticast():
		return false
	case addr.IsPrivate():
		// ip_is_private → direct keeps the LAN, the router and the local
		// resolver reachable. A rule here would take that away.
		return false
	case fakeIPRange.Contains(addr):
		return false
	}
	return true
}
