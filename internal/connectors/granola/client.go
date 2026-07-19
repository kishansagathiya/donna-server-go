package granola

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
)

// MCPCaller abstracts the Granola MCP session for tests.
type MCPCaller interface {
	ListTools(ctx context.Context) ([]string, error)
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	Close() error
}

type liveCaller struct {
	session *mcp.ClientSession
}

func (c *liveCaller) ListTools(ctx context.Context) ([]string, error) {
	res, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Tools))
	for _, t := range res.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

func (c *liveCaller) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", fmt.Errorf("mcp tool error: %s", textFromContent(res.Content))
	}
	return textFromContent(res.Content), nil
}

func (c *liveCaller) Close() error {
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

func textFromContent(content []mcp.Content) string {
	var parts []string
	for _, c := range content {
		switch v := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, v.Text)
		default:
			b, _ := json.Marshal(v)
			parts = append(parts, string(b))
		}
	}
	return strings.Join(parts, "\n")
}

// OpenMCP connects to the fixed Granola Streamable HTTP endpoint with the user's token.
func (a *Adapter) OpenMCP(ctx context.Context, conn connectors.Connection) (MCPCaller, error) {
	if a.MCPFactory != nil {
		return a.MCPFactory(ctx, conn)
	}
	if conn.AccessToken == "" {
		return nil, fmt.Errorf("missing access token")
	}
	handler := &staticOAuthHandler{
		token: &oauth2.Token{
			AccessToken:  conn.AccessToken,
			RefreshToken: conn.RefreshToken,
			Expiry:       conn.TokenExpiry,
			TokenType:    "Bearer",
		},
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:     MCPEndpoint,
		HTTPClient:   a.HTTPClient,
		OAuthHandler: handler,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "donna", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	return &liveCaller{session: session}, nil
}

// AccountInfo is the parsed get_account_info payload.
type AccountInfo struct {
	Email     string `json:"email"`
	Workspace string `json:"workspace"`
	// Some responses nest workspace.
	ActiveWorkspace string `json:"active_workspace"`
	WorkspaceID     string `json:"workspace_id"`
	WorkspaceName   string `json:"workspace_name"`
}

func parseAccountInfo(raw string) AccountInfo {
	var info AccountInfo
	_ = json.Unmarshal([]byte(raw), &info)
	if info.Workspace == "" {
		info.Workspace = firstNonEmpty(info.ActiveWorkspace, info.WorkspaceName, info.WorkspaceID)
	}
	// Also try nested shapes.
	var nested map[string]any
	if err := json.Unmarshal([]byte(raw), &nested); err == nil {
		if info.Email == "" {
			if v, ok := nested["email"].(string); ok {
				info.Email = v
			}
		}
		if info.Workspace == "" {
			for _, key := range []string{"workspace", "active_workspace", "workspace_name", "workspace_id"} {
				if v, ok := nested[key].(string); ok && v != "" {
					info.Workspace = v
					break
				}
				if m, ok := nested[key].(map[string]any); ok {
					if name, ok := m["name"].(string); ok && name != "" {
						info.Workspace = name
						break
					}
					if id, ok := m["id"].(string); ok && id != "" {
						info.Workspace = id
						break
					}
				}
			}
		}
	}
	if info.Email == "" && info.Workspace == "" {
		// Fall back to raw for identity hashing — still detectable on change.
		info.Workspace = strings.TrimSpace(raw)
	}
	return info
}

func workspaceIdentity(info AccountInfo) string {
	return strings.TrimSpace(info.Email) + "|" + strings.TrimSpace(info.Workspace)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// withRetry retries on 429 / transient errors, honoring Retry-After when present.
func withRetry(ctx context.Context, attempts int, fn func() error) error {
	if attempts <= 0 {
		attempts = 3
	}
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "401") || strings.Contains(msg, "reauth") {
			return err
		}
		if i == attempts-1 {
			break
		}
		wait := time.Duration(1<<i) * time.Second
		if strings.Contains(msg, "429") || strings.Contains(msg, "rate") {
			wait = time.Duration(2<<i) * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}
