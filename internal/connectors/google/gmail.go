package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const gmailSendURL = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"

func (a *Adapter) sendEmail(ctx context.Context, accessToken string, input map[string]any) (map[string]any, error) {
	to := firstNonEmpty(
		stringSlot(input, "to"),
		stringSlot(input, "recipient"),
	)
	if to == "" {
		return nil, fmt.Errorf("recipient_required")
	}
	body := stringSlot(input, "body")
	if body == "" {
		return nil, fmt.Errorf("body_required")
	}
	subject := firstNonEmpty(
		stringSlot(input, "subject"),
		stringSlot(input, "summary"),
		"Message from Donna",
	)
	cc := stringSlot(input, "cc")

	raw, err := buildRFC822Message(to, cc, subject, body)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]string{
		"raw": base64.RawURLEncoding.EncodeToString(raw),
	})
	if err != nil {
		return nil, err
	}

	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gmailSendURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("reauth_required")
	}
	if res.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("needs_integration:google")
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("gmail_send_failed:%d", res.StatusCode)
	}

	var created struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, err
	}

	return map[string]any{
		"type":       "send_email",
		"provider":   "google",
		"message_id": created.ID,
		"thread_id":  created.ThreadID,
		"to":         to,
		"cc":         cc,
		"subject":    subject,
		"sent":       true,
		"sent_at":    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func buildRFC822Message(to, cc, subject, body string) ([]byte, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return nil, fmt.Errorf("recipient_required")
	}
	var b strings.Builder
	b.WriteString("To: ")
	b.WriteString(sanitizeHeader(to))
	b.WriteString("\r\n")
	if strings.TrimSpace(cc) != "" {
		b.WriteString("Cc: ")
		b.WriteString(sanitizeHeader(cc))
		b.WriteString("\r\n")
	}
	b.WriteString("Subject: ")
	b.WriteString(sanitizeHeader(subject))
	b.WriteString("\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\r\n")
	}
	return []byte(b.String()), nil
}

func sanitizeHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}
