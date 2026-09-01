package agents

import (
	"context"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

// BrowserSession is the harness-facing browser surface. Cloud uses the
// donna-browser HTTP sidecar; desktop uses a local Playwright service that
// speaks the same protocol.
type BrowserSession interface {
	SessionCloser
	Navigate(ctx context.Context, sessionID, rawURL string, waitMs int) (tools.BrowserSnapshot, error)
	Snapshot(ctx context.Context, sessionID string) (tools.BrowserSnapshot, error)
	Click(ctx context.Context, sessionID, ref string) (tools.BrowserSnapshot, error)
	Type(ctx context.Context, sessionID, ref, text string, submit bool) (tools.BrowserSnapshot, error)
}

var _ BrowserSession = (*tools.BrowserClient)(nil)
