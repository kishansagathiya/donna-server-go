package actions

import "testing"

func TestIsRetryableIntegrationError(t *testing.T) {
	if !isRetryableIntegrationError("needs_integration:google") {
		t.Fatal("expected needs_integration:google to be retryable")
	}
	if !isRetryableIntegrationError("reauth_required") {
		t.Fatal("expected reauth_required to be retryable")
	}
	if isRetryableIntegrationError("title_required") {
		t.Fatal("title_required should not be retryable")
	}
}
