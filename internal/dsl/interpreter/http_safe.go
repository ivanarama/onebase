package interpreter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// safeHTTPPolicy is the operator-owned boundary for requests whose URL comes
// from untrusted data. A policy names exactly one host and, optionally, one
// port. Without an explicit port only the scheme defaults (http:80 and
// https:443) are accepted.
type safeHTTPPolicy struct {
	host         string
	port         string
	explicitPort bool
}

func parseSafeHTTPPolicy(raw string) (safeHTTPPolicy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return safeHTTPPolicy{}, errors.New("разрешённый источник не указан")
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#@") {
		return safeHTTPPolicy{}, errors.New("источник должен быть доменом или доменом с портом")
	}

	host, port := raw, ""
	explicitPort := false
	switch {
	case strings.HasPrefix(raw, "["):
		if strings.HasSuffix(raw, "]") {
			host = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
		} else {
			var err error
			host, port, err = net.SplitHostPort(raw)
			if err != nil {
				return safeHTTPPolicy{}, fmt.Errorf("некорректный источник: %w", err)
			}
			explicitPort = true
		}
	case net.ParseIP(raw) != nil:
		// Bare IPv6 without a port is unambiguous because it parses as an IP.
		host = raw
	case strings.Count(raw, ":") == 1:
		var err error
		host, port, err = net.SplitHostPort(raw)
		if err != nil {
			return safeHTTPPolicy{}, fmt.Errorf("некорректный источник: %w", err)
		}
		explicitPort = true
	case strings.Contains(raw, ":"):
		return safeHTTPPolicy{}, errors.New("IPv6-адрес с портом должен быть заключён в квадратные скобки")
	}

	host = canonicalSafeHTTPHost(host)
	if host == "" || strings.Contains(host, "%") {
		return safeHTTPPolicy{}, errors.New("некорректный хост источника")
	}
	if explicitPort {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return safeHTTPPolicy{}, errors.New("порт источника должен быть числом от 1 до 65535")
		}
	}
	return safeHTTPPolicy{host: host, port: port, explicitPort: explicitPort}, nil
}

func canonicalSafeHTTPHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String()
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(ascii), ".")
}

// matchesURL validates the syntactic allowlist. IP safety is deliberately a
// separate step: a domain can only be classified after DNS resolution.
func (p safeHTTPPolicy) matchesURL(u *url.URL) error {
	if u == nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" {
		return errors.New("нужен абсолютный HTTP-адрес")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("разрешены только схемы http и https")
	}
	if u.User != nil {
		return errors.New("учётные данные в адресе запрещены")
	}
	host := canonicalSafeHTTPHost(u.Hostname())
	if host == "" || host != p.host {
		return errors.New("хост адреса не совпадает с разрешённым источником")
	}
	port := u.Port()
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	if p.explicitPort {
		if port != p.port {
			return errors.New("порт адреса не совпадает с разрешённым источником")
		}
		return nil
	}
	if (scheme == "http" && port != "80") || (scheme == "https" && port != "443") {
		return errors.New("нестандартный порт должен быть явно указан оператором")
	}
	return nil
}

func (p safeHTTPPolicy) validatesLiteralHost(u *url.URL) error {
	host := canonicalSafeHTTPHost(u.Hostname())
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errors.New("локальный хост запрещён")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil // DNS names are classified by safeHTTPDialer after resolution.
	}
	if !isPubliclyRoutableIP(addr) {
		return fmt.Errorf("непубличный IP-адрес %s запрещён", addr)
	}
	return nil
}

var nonPublicIPPrefixes = []netip.Prefix{
	// IPv4 special-use networks (RFC 6890 and successors).
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("168.63.129.16/32"), // Azure WireServer / host platform endpoint
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),

	// IPv6 special-use networks. Mapped IPv4 is Unmap'ed before this table.
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::/96"),           // deprecated IPv4-compatible addresses
	netip.MustParsePrefix("::ffff:0:0:0/96"), // IPv4-translatable addresses
	netip.MustParsePrefix("64:ff9b::/96"),    // well-known NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("100:0:0:1::/64"),  // dummy IPv6 prefix
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4 (can embed a private IPv4)
	netip.MustParsePrefix("3ffe::/16"),       // deprecated 6bone allocation
	netip.MustParsePrefix("3fff::/20"),       // documentation
	netip.MustParsePrefix("5f00::/16"),       // segment-routing SIDs
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fec0::/10"), // deprecated site-local
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

var currentlyRoutableIPv6 = netip.MustParsePrefix("2000::/3")

func isPubliclyRoutableIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	// IANA currently allocates public unicast IPv6 from 2000::/3. Fail closed
	// outside that range: it also covers deprecated translation/site-local and
	// newly assigned special-use prefixes which a static denylist could miss.
	if addr.Is6() && !currentlyRoutableIPv6.Contains(addr) {
		return false
	}
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicIPPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

type safeHTTPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type safeHTTPDialer struct {
	policy   safeHTTPPolicy
	resolver safeHTTPResolver
	dial     func(context.Context, string, string) (net.Conn, error)
}

func (d safeHTTPDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("безопасный HTTP: некорректная точка назначения: %w", err)
	}
	if canonicalSafeHTTPHost(host) != d.policy.host {
		return nil, errors.New("безопасный HTTP: transport попытался подключиться не к разрешённому хосту")
	}
	if d.policy.explicitPort {
		if port != d.policy.port {
			return nil, errors.New("безопасный HTTP: transport попытался подключиться не к разрешённому порту")
		}
	} else if port != "80" && port != "443" {
		return nil, errors.New("безопасный HTTP: transport попытался подключиться к нестандартному порту")
	}

	var resolved []net.IPAddr
	if literal, parseErr := netip.ParseAddr(canonicalSafeHTTPHost(host)); parseErr == nil {
		resolved = []net.IPAddr{{IP: net.IP(literal.AsSlice())}}
	} else {
		resolved, err = d.resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("безопасный HTTP: DNS %s: %w", host, err)
		}
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("безопасный HTTP: DNS %s не вернул адресов", host)
	}

	// Reject the whole answer if it contains even one non-public address. Merely
	// filtering it would make security depend on resolver ordering and would let
	// a rebinding name oscillate between public and private answers.
	addrs := make([]netip.Addr, 0, len(resolved))
	seen := map[netip.Addr]bool{}
	for _, candidate := range resolved {
		if candidate.Zone != "" {
			return nil, fmt.Errorf("безопасный HTTP: зональный IP-адрес %s%%%s запрещён", candidate.IP, candidate.Zone)
		}
		addr, ok := netip.AddrFromSlice(candidate.IP)
		if !ok {
			return nil, fmt.Errorf("безопасный HTTP: DNS %s вернул некорректный IP", host)
		}
		addr = addr.Unmap()
		if !isPubliclyRoutableIP(addr) {
			return nil, fmt.Errorf("безопасный HTTP: DNS %s вернул непубличный IP-адрес %s", host, addr)
		}
		if !seen[addr] {
			seen[addr] = true
			addrs = append(addrs, addr)
		}
	}

	// The validated numeric address, not the hostname, is passed to net.Dialer.
	// There is no second DNS lookup between validation and the actual connect.
	var dialErr error
	for _, addr := range addrs {
		conn, err := d.dial(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = errors.Join(dialErr, err)
	}
	return nil, fmt.Errorf("безопасный HTTP: соединение с %s: %w", host, dialErr)
}

func newSafeHTTPClient(policy safeHTTPPolicy, resolver safeHTTPResolver,
	dial func(context.Context, string, string) (net.Conn, error), timeout time.Duration,
) (*http.Client, *http.Transport) {
	// Keep the security transport self-contained. Cloning http.DefaultTransport
	// would inherit process-global DialTLS/DialTLSContext hooks; either hook
	// bypasses DialContext for HTTPS and therefore our DNS validation/pinning.
	transport := &http.Transport{
		Proxy:                  nil, // An environment proxy would dial outside our pinning boundary.
		DialContext:            safeHTTPDialer{policy: policy, resolver: resolver, dial: dial}.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		MaxResponseHeaderBytes: 1 << 20,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("безопасный HTTP: слишком много перенаправлений")
			}
			if err := policy.matchesURL(req.URL); err != nil {
				return fmt.Errorf("безопасный HTTP: перенаправление запрещено: %w", err)
			}
			if err := policy.validatesLiteralHost(req.URL); err != nil {
				return fmt.Errorf("безопасный HTTP: перенаправление запрещено: %w", err)
			}
			return nil
		},
	}
	return client, transport
}

// matchSafeHTTPURL separates a harmless allowlist miss (matched=false) from an
// unsafe literal that did match the operator's authority (matched=true, err).
// The DSL importer uses the former for its "foreign source" counter and reports
// the latter as a real download failure.
func matchSafeHTTPURL(rawURL, rawPolicy string) (safeHTTPPolicy, *url.URL, bool, error) {
	policy, err := parseSafeHTTPPolicy(rawPolicy)
	if err != nil {
		return safeHTTPPolicy{}, nil, false, nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || policy.matchesURL(u) != nil {
		return safeHTTPPolicy{}, nil, false, nil
	}
	if err := policy.validatesLiteralHost(u); err != nil {
		return policy, u, true, err
	}
	return policy, u, true, nil
}

func safeHTTPURLAllowed(rawURL, rawPolicy string) bool {
	_, _, matched, err := matchSafeHTTPURL(rawURL, rawPolicy)
	return matched && err == nil
}

func performSafeHTTPGet(ctx context.Context, policy safeHTTPPolicy, u *url.URL) (*dslHTTPResponse, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	client, transport := newSafeHTTPClient(policy, net.DefaultResolver, dialer.DialContext, 30*time.Second)
	defer transport.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) //nolint:gosec // G704: URL passed the exact-authority and public-IP checks above.
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req) //nolint:gosec // G704: the custom transport pins every dial to a validated public numeric IP.
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // response is fully consumed below
	body := readDSLHTTPResponseContext(ctx, resp.Body, "HTTPПолучитьБезопасно")
	return &dslHTTPResponse{statusCode: resp.StatusCode, headers: resp.Header, body: string(body)}, nil
}
