package google

import "testing"

func TestMapGoogleAPIErrorScopeInsufficient(t *testing.T) {
	body := []byte(`{"error":{"code":403,"message":"Request had insufficient authentication scopes.","status":"PERMISSION_DENIED","details":[{"reason":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}]}}`)
	err := mapGoogleAPIError("calendar_create", 403, body)
	if err == nil || err.Error() != "reauth_required" {
		t.Fatalf("got %v", err)
	}
}

func TestMapGoogleAPIErrorAPINotEnabled(t *testing.T) {
	body := []byte(`{"error":{"code":403,"message":"Google Calendar API has not been used in project 123 before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`)
	err := mapGoogleAPIError("calendar_create", 403, body)
	if err == nil || err.Error() != "google_api_not_enabled:calendar" {
		t.Fatalf("got %v", err)
	}
}
