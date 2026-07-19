package chat

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	maxChatAttachments     = 5
	maxChatAttachmentBytes = 15 * 1024 * 1024
	maxAttachmentTextChars = 50_000
)

// ChatAttachment is an in-turn grounding payload (ephemeral; not knowledge ingest).
type ChatAttachment struct {
	Kind       string `json:"kind"` // "file" | "url"
	Filename   string `json:"filename,omitempty"`
	Mime       string `json:"mime,omitempty"`
	DataBase64 string `json:"data_base64,omitempty"`
	URL        string `json:"url,omitempty"`
}

type groundedTurn struct {
	DisplayMessage  string
	GroundedMessage string
	Labels          []string
}

func groundChatTurn(message string, attachments []ChatAttachment) (groundedTurn, error) {
	message = strings.TrimSpace(message)
	if len(attachments) > maxChatAttachments {
		return groundedTurn{}, fmt.Errorf("too many attachments (max %d)", maxChatAttachments)
	}

	var (
		blocks []string
		labels []string
	)

	for i, att := range attachments {
		label, content, err := extractAttachment(att)
		if err != nil {
			return groundedTurn{}, fmt.Errorf("attachment %d: %w", i+1, err)
		}
		labels = append(labels, label)
		blocks = append(blocks, fmt.Sprintf("Attached: %s\n\n%s", label, content))
	}

	// If the user pasted a bare URL with no explicit url attachment, fetch it.
	if len(attachments) == 0 {
		if u := loneURL(message); u != "" {
			label, content, err := extractURLAttachment(u)
			if err != nil {
				return groundedTurn{}, err
			}
			labels = append(labels, label)
			blocks = append(blocks, fmt.Sprintf("Attached: %s\n\n%s", label, content))
		}
	}

	if message == "" && len(blocks) == 0 {
		return groundedTurn{}, fmt.Errorf("message cannot be empty")
	}

	// Regenerate/retry may re-send a previously grounded transcript with no
	// attachment payloads. Recover the short user-facing prompt so we don't
	// persist the vision dump as user_transcript again.
	if len(blocks) == 0 {
		if display, groundedMsg, ok := splitGroundedTranscript(message); ok {
			return groundedTurn{
				DisplayMessage:  display,
				GroundedMessage: groundedMsg,
				Labels:          labels,
			}, nil
		}
	}

	display := message
	if len(labels) > 0 {
		joined := strings.Join(labels, ", ")
		if display == "" {
			display = "📎 " + joined
		} else {
			display = display + "\n\n📎 " + joined
		}
	}

	grounded := message
	if len(blocks) > 0 {
		joined := strings.Join(blocks, "\n\n---\n\n")
		if grounded == "" {
			grounded = groundedAttachmentPreamble + joined
		} else {
			grounded = grounded + "\n\n" + groundedAttachmentPreamble + joined
		}
	}

	return groundedTurn{
		DisplayMessage:  display,
		GroundedMessage: grounded,
		Labels:          labels,
	}, nil
}

const groundedAttachmentPreamble = "The user shared the following attachment(s) for this turn only (not saved to long-term memory unless they ask):\n\n"

func splitGroundedTranscript(message string) (display, grounded string, ok bool) {
	grounded = strings.TrimSpace(message)
	if grounded == "" {
		return "", "", false
	}
	idx := strings.Index(grounded, "The user shared the following attachment(s) for this turn only")
	if idx < 0 {
		return "", "", false
	}
	before := strings.TrimSpace(grounded[:idx])
	if before != "" {
		return before, grounded, true
	}
	var labels []string
	for _, line := range strings.Split(grounded, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Attached:") {
			label := strings.TrimSpace(strings.TrimPrefix(line, "Attached:"))
			if label != "" {
				labels = append(labels, label)
			}
		}
	}
	if len(labels) == 0 {
		return "📎 attachment", grounded, true
	}
	return "📎 " + strings.Join(labels, ", "), grounded, true
}

func extractAttachment(att ChatAttachment) (label, content string, err error) {
	kind := strings.ToLower(strings.TrimSpace(att.Kind))
	switch kind {
	case "url":
		return extractURLAttachment(att.URL)
	case "file", "image", "":
		return extractFileAttachment(att)
	default:
		return "", "", fmt.Errorf("unsupported attachment kind %q", att.Kind)
	}
}

func extractFileAttachment(att ChatAttachment) (label, content string, err error) {
	buf, err := decodeAttachmentBase64(att.DataBase64)
	if err != nil {
		return "", "", err
	}
	if len(buf) == 0 {
		return "", "", fmt.Errorf("empty file")
	}
	if len(buf) > maxChatAttachmentBytes {
		return "", "", fmt.Errorf("file too large (max 15MB)")
	}

	filename := strings.TrimSpace(att.Filename)
	if filename == "" {
		filename = "attachment"
	}
	extracted, err := ingest.DispatchFileExtraction(buf, att.Mime, filename)
	if err != nil {
		return "", "", err
	}
	text := clampAttachmentText(extracted.Content)
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("no content extracted from %s", filename)
	}
	return filename, text, nil
}

func decodeAttachmentBase64(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("file attachment requires data_base64")
	}
	// Allow data URL prefix if clients send one.
	if idx := strings.Index(raw, "base64,"); idx >= 0 {
		raw = raw[idx+len("base64,"):]
	}
	buf, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// Some clients use raw URL-encoding-safe base64.
		buf, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 data")
		}
	}
	return buf, nil
}

// toSaveTurnAttachments copies request attachments into storage upload payloads.
func toSaveTurnAttachments(attachments []ChatAttachment) []storage.SaveTurnAttachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]storage.SaveTurnAttachment, 0, len(attachments))
	for _, att := range attachments {
		kind := strings.ToLower(strings.TrimSpace(att.Kind))
		filename := strings.TrimSpace(att.Filename)
		switch kind {
		case "url":
			u := strings.TrimSpace(att.URL)
			if u == "" {
				continue
			}
			if filename == "" {
				filename = u
			}
			out = append(out, storage.SaveTurnAttachment{
				Kind:     "url",
				Filename: filename,
				URL:      u,
			})
		case "file", "image", "":
			data, err := decodeAttachmentBase64(att.DataBase64)
			if err != nil || len(data) == 0 {
				continue
			}
			if filename == "" {
				filename = "attachment"
			}
			out = append(out, storage.SaveTurnAttachment{
				Kind:     "file",
				Filename: filename,
				Mime:     strings.TrimSpace(att.Mime),
				Data:     data,
			})
		}
	}
	return out
}

func extractURLAttachment(rawURL string) (label, content string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", fmt.Errorf("url attachment requires url")
	}
	if err := assertPublicHTTPURL(rawURL); err != nil {
		return "", "", err
	}
	extracted, err := ingest.ExtractURL(rawURL)
	if err != nil {
		return "", "", err
	}
	text := clampAttachmentText(extracted.Content)
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("no text content extracted from URL")
	}
	label = extracted.Title
	if label == "" {
		label = rawURL
	}
	return label, text, nil
}

func clampAttachmentText(text string) string {
	if len(text) <= maxAttachmentTextChars {
		return text
	}
	return text[:maxAttachmentTextChars] + "\n\n[truncated]"
}

func loneURL(message string) string {
	fields := strings.Fields(message)
	if len(fields) != 1 {
		return ""
	}
	candidate := strings.Trim(fields[0], "<>")
	if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
		return candidate
	}
	return ""
}

func assertPublicHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("only HTTP/HTTPS URLs are supported")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("invalid URL host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" {
		return fmt.Errorf("URL host is not allowed")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		// Let ExtractURL surface fetch errors for unresolvable hosts.
		return nil
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL host is not allowed")
		}
	}
	return nil
}
