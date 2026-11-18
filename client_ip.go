package httpserver

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

type IpType int

var trustAllCIDRs = []*net.IPNet{
	{
		IP:   net.ParseIP("0.0.0.0"),
		Mask: net.CIDRMask(0, 32),
	},
	{
		IP:   net.ParseIP("::"),
		Mask: net.CIDRMask(0, 128),
	},
}

const (
	// RemoteIp describes the IP of the actual caller of the service. This requires you to configure the correct set of trusted CIDRs for your HTTP server.
	// Use this IP type when making security-relevant decisions based off an IP address.
	RemoteIp IpType = iota
	// ForwardedIp describes the assumed IP of the user behind a transparent proxy. This trusts any proxy in-between the user and your server.
	// Use this IP type when optimizing for tracking precision and a wrong decision has no security impact.
	ForwardedIp
)

// ResolveClientIP resolves the caller IP from forwarded headers and falls back to req.RemoteAddr.
func ResolveClientIP(req *http.Request, ipType IpType) (string, error) {
	var err error
	var remoteAddr string

	if remoteAddr, err = remoteIP(req); err != nil {
		return "", err
	}

	remoteAddrIP := net.ParseIP(remoteAddr)
	if remoteAddrIP == nil {
		return "", fmt.Errorf("invalid remote address IP: %q", remoteAddr)
	}

	var trustedProxies []*net.IPNet
	if ipType == ForwardedIp {
		trustedProxies = trustAllCIDRs
	} else {
		trustedProxies = getTrustedProxies(req.Context())
	}

	// validate trust root, if our direct remote IP is not trusted, nothing else is and this is our result
	if !isTrustedProxy(remoteAddrIP, trustedProxies) {
		return remoteAddrIP.String(), nil
	}

	for _, headerName := range []string{HeaderXForwardedFor, HeaderXRealIP} {
		if ip, valid := validateClientIPHeader(req.Header.Values(headerName), trustedProxies); valid {
			return ip, nil
		}
	}

	return remoteAddrIP.String(), nil
}

func remoteIP(req *http.Request) (string, error) {
	var err error
	var ip string

	if ip, _, err = net.SplitHostPort(strings.TrimSpace(req.RemoteAddr)); err != nil {
		return "", err
	}

	return ip, nil
}

func validateClientIPHeader(headers []string, trustedCIDRs []*net.IPNet) (clientIP string, valid bool) {
	var header string
	switch len(headers) {
	case 0:
		header = ""
	case 1:
		header = headers[0]
	default:
		// treat multiple headers like a single large joined one
		header = strings.Join(headers, ",")
	}

	if header == "" {
		return "", false
	}

	// items holds the possible IP values ordered from most distant to closest proxy, i.e.,
	// User -> Proxy -> Load Balancer -> Server results in "User IP, Proxy IP" with
	// Load Balancer IP as the remote addr we receive the request from at the server.
	items := strings.Split(header, ",")
	for i := len(items) - 1; i >= 0; i-- {
		ip := parseIp(items[i])

		if ip == nil {
			// any invalid IP breaks the trust chain, bail
			break
		}

		if i == 0 || !isTrustedProxy(ip, trustedCIDRs) {
			return ip.String(), true
		}
	}

	return "", false
}

func parseIp(ip string) net.IP {
	ipStr := strings.TrimSpace(ip)
	// some load balancers forward IP:Port instead of just IP
	if hostPart, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = hostPart
	}

	return net.ParseIP(ipStr)
}

func isTrustedProxy(ip net.IP, trustedCIDRs []*net.IPNet) bool {
	for _, cidr := range trustedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}
