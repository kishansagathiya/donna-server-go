package actions

import (
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

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

func TestShouldResumeAgent(t *testing.T) {
	id := "run-1"
	kind := "book_flight"
	if shouldResumeAgent(storage.ActionRun{AgentRunID: &id, ApprovalKind: &kind}) != true {
		t.Fatal("expected resume")
	}
	if shouldResumeAgent(storage.ActionRun{AgentRunID: &id}) {
		t.Fatal("calendar-style agent_run_id without approval_kind must not resume")
	}
	if shouldResumeAgent(storage.ActionRun{ApprovalKind: &kind}) {
		t.Fatal("missing agent_run_id")
	}
}
