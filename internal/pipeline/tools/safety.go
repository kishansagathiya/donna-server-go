package tools

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidatePublicURL rejects non-HTTP(S) schemes and SSRF-prone targets
// (localhost, private/link-local/metadata IPs). Host is resolved so DNS
// rebinding to private addresses is blocked at validation time.
func ValidatePublicURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only http and https urls are allowed")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return nil, fmt.Errorf("url host is required")
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || lower == "0.0.0.0" {
		return nil, fmt.Errorf("localhost urls are not allowed")
	}

	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("private or reserved ips are not allowed")
		}
		return parsed, nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("could not resolve host: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("could not resolve host")
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("host resolves to a private or reserved ip")
		}
	}
	return parsed, nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Cloud metadata / common non-routable ranges not always covered above.
	if ip4 := ip.To4(); ip4 != nil {
		// 169.254.0.0/16 link-local already covered; 100.64.0.0/10 CGNAT
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
	}
	return true
}
