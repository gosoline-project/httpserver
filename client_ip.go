package httpserver

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

var defaultTrustedCIDRs, defaultTrustedCIDRsErr = parseCIDRs([]string{"0.0.0.0/0", "::/0"})

func ResolveClientIP(req *http.Request) (string, error) {
	var err error
	var remoteAddr string

	if defaultTrustedCIDRsErr != nil {
		return "", defaultTrustedCIDRsErr
	}

	if remoteAddr, err = remoteIP(req); err != nil {
		return "", err
	}

	remoteAddrIP := net.ParseIP(remoteAddr)
	if remoteAddrIP == nil {
		return "", fmt.Errorf("invalid remote address IP: %q", remoteAddr)
	}

	for _, headerName := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if ip, valid := validateClientIPHeader(req.Header.Get(headerName), defaultTrustedCIDRs); valid {
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

func validateClientIPHeader(header string, trustedCIDRs []*net.IPNet) (clientIP string, valid bool) {
	if header == "" {
		return "", false
	}

	items := strings.Split(header, ",")
	for i := len(items) - 1; i >= 0; i-- {
		ipStr := strings.TrimSpace(items[i])
		ip := net.ParseIP(ipStr)

		if ip == nil {
			break
		}

		if i == 0 || !isTrustedProxy(ip, trustedCIDRs) {
			return ipStr, true
		}
	}

	return "", false
}

func isTrustedProxy(ip net.IP, trustedCIDRs []*net.IPNet) bool {
	for _, cidr := range trustedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	var err error
	var cidr *net.IPNet
	cidrs := make([]*net.IPNet, 0, len(values))

	for _, value := range values {
		if _, cidr, err = net.ParseCIDR(value); err != nil {
			return nil, fmt.Errorf("parse trusted CIDR %q: %w", value, err)
		}

		cidrs = append(cidrs, cidr)
	}

	return cidrs, nil
}
