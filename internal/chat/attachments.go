package chat

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	maxChatAttachments         = 10
	maxChatAttachmentBytes     = 15 * 1024 * 1024
	maxAttachmentTextChars     = 50_000
	maxParallelChatAttachments = 4
	maxChatRequestBodyBytes    = 32 << 20
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
	Captures        []ingest.ExtractedAsset
}

// GroundedTurn is the exported grounding result for other packages (e.g. agents).
type GroundedTurn struct {
	DisplayMessage  string
	GroundedMessage string
	Labels          []string
}

// GroundChatTurn grounds a user message plus optional attachments into LLM-facing text.
func GroundChatTurn(ctx context.Context, message string, attachments []ChatAttachment) (GroundedTurn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	g, err := groundChatTurn(ctx, message, attachments)
	if err != nil {
		return GroundedTurn{}, err
	}
	return GroundedTurn{
		DisplayMessage:  g.DisplayMessage,
		GroundedMessage: g.GroundedMessage,
		Labels:          g.Labels,
	}, nil
}

func groundChatTurn(ctx context.Context, message string, attachments []ChatAttachment) (groundedTurn, error) {
	message = strings.TrimSpace(message)
	if len(attachments) > maxChatAttachments {
		return groundedTurn{}, fmt.Errorf("too many attachments (max %d)", maxChatAttachments)
	}

	extracted, err := extractAttachments(ctx, attachments)
	if err != nil {
		return groundedTurn{}, err
	}

	var (
		blocks   []string
		labels   []string
		captures []ingest.ExtractedAsset
	)

	for _, item := range extracted {
		labels = append(labels, item.label)
		blocks = append(blocks, fmt.Sprintf("Attached: %s\n\n%s", item.label, item.content))
		if isPersistableCapture(item.extracted) {
			captures = append(captures, item.extracted)
		}
	}

	// Fetch a bare URL, and always fetch Twitter/X status links so they can
	// be saved into memory even when mixed with commentary.
	if len(attachments) == 0 {
		tweetURLs := ingest.FindTweetURLs(message)
		if len(tweetURLs) > 0 {
			for _, u := range tweetURLs {
				label, content, extracted, err := extractURLAttachment(u)
				if err != nil {
					continue
				}
				labels = append(labels, label)
				blocks = append(blocks, fmt.Sprintf("Attached: %s\n\n%s", label, content))
				if isPersistableCapture(extracted) {
					captures = append(captures, extracted)
				}
			}
		} else if u := loneURL(message); u != "" {
			label, content, extracted, err := extractURLAttachment(u)
			if err != nil {
				return groundedTurn{}, err
			}
			labels = append(labels, label)
			blocks = append(blocks, fmt.Sprintf("Attached: %s\n\n%s", label, content))
			if isPersistableCapture(extracted) {
				captures = append(captures, extracted)
			}
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
		preamble := groundedAttachmentPreamble
		if len(captures) > 0 {
			preamble = groundedCapturePreamble
		}
		if grounded == "" {
			grounded = preamble + joined
		} else {
			grounded = grounded + "\n\n" + preamble + joined
		}
	}

	return groundedTurn{
		DisplayMessage:  display,
		GroundedMessage: grounded,
		Labels:          labels,
		Captures:        captures,
	}, nil
}

const groundedAttachmentPreamble = "The user shared the following attachment(s) for this turn only (not saved to long-term memory unless they ask):\n\n"
const groundedCapturePreamble = "The user captured the following link(s) to save into memory:\n\n"

func splitGroundedTranscript(message string) (display, grounded string, ok bool) {
	grounded = strings.TrimSpace(message)
	if grounded == "" {
		return "", "", false
	}
	idx := strings.Index(grounded, "The user shared the following attachment(s) for this turn only")
	if idx < 0 {
		idx = strings.Index(grounded, "The user captured the following link(s) to save into memory")
	}
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

type attachmentResult struct {
	label     string
	content   string
	extracted ingest.ExtractedAsset
}

// extractOneAttachment is swapped in tests to prove parallel extraction.
var extractOneAttachment = extractAttachment

func extractAttachments(ctx context.Context, attachments []ChatAttachment) ([]attachmentResult, error) {
	n := len(attachments)
	if n == 0 {
		return nil, nil
	}
	if n == 1 {
		label, content, extracted, err := extractOneAttachment(ctx, attachments[0])
		if err != nil {
			return nil, fmt.Errorf("attachment 1: %w", err)
		}
		return []attachmentResult{{label: label, content: content, extracted: extracted}}, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out := make([]attachmentResult, n)
	errCh := make(chan error, 1)
	sem := make(chan struct{}, maxParallelChatAttachments)
	var wg sync.WaitGroup

	for i, att := range attachments {
		wg.Add(1)
		go func(i int, att ChatAttachment) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}
			label, content, extracted, err := extractOneAttachment(ctx, att)
			if err != nil {
				select {
				case errCh <- fmt.Errorf("attachment %d: %w", i+1, err):
					cancel()
				default:
				}
				return
			}
			out[i] = attachmentResult{label: label, content: content, extracted: extracted}
		}(i, att)
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
		return out, nil
	}
}

func extractAttachment(ctx context.Context, att ChatAttachment) (label, content string, extracted ingest.ExtractedAsset, err error) {
	kind := strings.ToLower(strings.TrimSpace(att.Kind))
	switch kind {
	case "url":
		label, content, extracted, err = extractURLAttachment(att.URL)
		return label, content, extracted, err
	case "file", "image", "":
		label, content, err = extractFileAttachment(ctx, att)
		return label, content, extracted, err
	default:
		return "", "", extracted, fmt.Errorf("unsupported attachment kind %q", att.Kind)
	}
}

func extractFileAttachment(ctx context.Context, att ChatAttachment) (label, content string, err error) {
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
	mime := ingest.ResolveMime(buf, att.Mime, filename)
	if strings.HasPrefix(mime, "image/") {
		return extractChatImage(ctx, att.DataBase64, mime, filename)
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

func extractChatImage(ctx context.Context, rawBase64, mime, filename string) (label, content string, err error) {
	desc, err := ingest.DescribeImage(ctx, ingest.ChatVisionPrompt, imageDataURL(mime, rawBase64))
	if err != nil {
		return "", "", err
	}
	text := clampAttachmentText(fmt.Sprintf("Image: %s\n\n%s", filename, desc))
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("no content extracted from %s", filename)
	}
	return filename, text, nil
}

func imageDataURL(mime, raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "data:") {
		return raw
	}
	if mime == "" {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + raw
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

func extractURLAttachment(rawURL string) (label, content string, extracted ingest.ExtractedAsset, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", extracted, fmt.Errorf("url attachment requires url")
	}
	if err := assertPublicHTTPURL(rawURL); err != nil {
		return "", "", extracted, err
	}
	extracted, err = ingest.ExtractURL(rawURL)
	if err != nil {
		return "", "", extracted, err
	}
	text := clampAttachmentText(extracted.Content)
	if strings.TrimSpace(text) == "" {
		return "", "", extracted, fmt.Errorf("no text content extracted from URL")
	}
	label = extracted.Title
	if label == "" {
		label = rawURL
	}
	return label, text, extracted, nil
}

func isPersistableCapture(extracted ingest.ExtractedAsset) bool {
	return strings.HasPrefix(extracted.Extractor, "twitter")
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
