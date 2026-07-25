package google

import (
	"encoding/json"
	"fmt"
	"strings"
)

type googleAPIErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"errors"`
		Details []struct {
			Reason   string `json:"reason"`
			Metadata map[string]string `json:"metadata"`
		} `json:"details"`
	} `json:"error"`
}

func mapGoogleAPIError(service string, statusCode int, body []byte) error {
	parsed := googleAPIErrorBody{}
	_ = json.Unmarshal(body, &parsed)

	reason := strings.TrimSpace(parsed.Error.Status)
	if reason == "" && len(parsed.Error.Errors) > 0 {
		reason = strings.TrimSpace(parsed.Error.Errors[0].Reason)
	}
	if reason == "" && len(parsed.Error.Details) > 0 {
		reason = strings.TrimSpace(parsed.Error.Details[0].Reason)
	}
	msg := strings.ToLower(parsed.Error.Message + " " + reason)
	for _, d := range parsed.Error.Details {
		msg += " " + strings.ToLower(d.Reason)
		for _, v := range d.Metadata {
			msg += " " + strings.ToLower(v)
		}
	}

	switch {
	case statusCode == 401:
		return fmt.Errorf("reauth_required")
	case strings.Contains(msg, "access_token_scope_insufficient"),
		strings.Contains(msg, "insufficientpermissions"),
		strings.Contains(msg, "insufficient permission"),
		strings.Contains(msg, "insufficient authentication scopes"):
		return fmt.Errorf("reauth_required")
	case strings.Contains(msg, "accessnotconfigured"),
		strings.Contains(msg, "has not been used"),
		strings.Contains(msg, "is disabled"),
		strings.Contains(msg, "api has not been enabled"),
		strings.Contains(msg, "service_disabled"):
		if service == "gmail" {
			return fmt.Errorf("google_api_not_enabled:gmail")
		}
		return fmt.Errorf("google_api_not_enabled:calendar")
	case statusCode == 403:
		if reason != "" {
			return fmt.Errorf("%s_failed:403:%s", service, sanitizeReason(reason))
		}
		return fmt.Errorf("%s_failed:403", service)
	default:
		if reason != "" {
			return fmt.Errorf("%s_failed:%d:%s", service, statusCode, sanitizeReason(reason))
		}
		return fmt.Errorf("%s_failed:%d", service, statusCode)
	}
}

func sanitizeReason(reason string) string {
	reason = strings.TrimSpace(reason)
	reason = strings.ReplaceAll(reason, " ", "_")
	if len(reason) > 64 {
		reason = reason[:64]
	}
	return reason
}
