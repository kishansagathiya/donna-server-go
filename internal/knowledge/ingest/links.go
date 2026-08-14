package ingest

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const maxExpandedLinks = 8

var httpURLRegexp = regexp.MustCompile(`https?://[^\s<>"'\)\]]+`)

var captureMarkers = []string{
	"User saved a tweet to memory.",
	"User saved a link to memory.",
}

// FindHTTPURLs returns unique http(s) URLs from the user-facing portion of text.
func FindHTTPURLs(text string) []string {
	matches := httpURLRegexp.FindAllString(userFacingText(text), -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, raw := range matches {
		cleaned := trimTrailingPunct(raw)
		parsed, err := url.Parse(cleaned)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		key := strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + parsed.RequestURI()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cleaned)
		if len(out) >= maxExpandedLinks {
			break
		}
	}
	return out
}

// ExpandLinks fetches every http(s) URL in the user-facing text and appends
// extracted page content. Tweets use the Twitter extractor; other pages use
// HTTP fetch, then the browser sidecar when the HTML is a JS shell.
// Fetch failures leave the original URL in place.
func ExpandLinks(content string) string {
	return expandLinks(content, ExtractURL)
}

func expandLinks(content string, fetch func(string) (ExtractedAsset, error)) string {
	urls := FindHTTPURLs(content)
	if len(urls) == 0 {
		return content
	}

	var bodies []string
	remaining := content
	for _, u := range urls {
		if alreadyExpandedLink(content, u) {
			remaining = strings.ReplaceAll(remaining, u, "")
			continue
		}
		if err := rejectPrivateURL(u); err != nil {
			continue
		}
		extracted, err := fetch(u)
		if err != nil {
			continue
		}
		bodies = append(bodies, formatCapturedLink(extracted))
		remaining = strings.ReplaceAll(remaining, u, "")
	}
	if len(bodies) == 0 {
		return content
	}

	rest := strings.TrimSpace(userFacingText(remaining))
	joined := strings.Join(bodies, "\n\n---\n\n")
	if rest == "" {
		return joined
	}
	return rest + "\n\n" + joined
}

func formatCapturedLink(extracted ExtractedAsset) string {
	body := strings.TrimSpace(extracted.Content)
	if strings.HasPrefix(extracted.Extractor, "twitter") {
		return body
	}
	if strings.Contains(body, "User saved a link to memory") {
		return body
	}
	return "User saved a link to memory.\n\n" + body
}

func alreadyExpandedLink(content, rawURL string) bool {
	if alreadyExpandedTweet(content, rawURL) {
		return true
	}
	return strings.Contains(content, "URL: "+rawURL)
}

func userFacingText(content string) string {
	cut := len(content)
	for _, marker := range captureMarkers {
		if i := strings.Index(content, marker); i >= 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(content[:cut])
}

func rejectPrivateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("only HTTP/HTTPS URLs are supported")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return fmt.Errorf("invalid URL host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "0.0.0.0" || host == "metadata.google.internal" {
		return fmt.Errorf("URL host is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("private or reserved ips are not allowed")
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("host resolves to a private or reserved ip")
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
	}
	return true
}
