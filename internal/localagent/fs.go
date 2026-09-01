package localagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

const (
	maxReadBytes   = 256 * 1024
	maxWriteBytes  = 512 * 1024
	maxListEntries = 200
)

func workspaceTools(root string) []agents.RegisteredTool {
	return []agents.RegisteredTool{
		listDirTool(root),
		readFileTool(root),
		searchFilesTool(root),
		writeFileTool(root),
		applyPatchTool(root),
		deleteFileTool(root),
	}
}

func listDirTool(root string) agents.RegisteredTool {
	return agents.RegisteredTool{
		Toolset: "workspace",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "list_dir",
				Description: "List files and directories inside the approved workspace. Path is relative to the workspace root.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Relative directory, default ."},
					},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *agents.RunContext, argsJSON string) (agents.ToolResult, error) {
			args, err := agents.ParseArgs[struct {
				Path string `json:"path"`
			}](argsJSON)
			if err != nil {
				return agents.ToolResult{}, err
			}
			dir, err := ResolveWorkspacePath(root, args.Path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			var b strings.Builder
			n := 0
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") && e.Name() != ".env.example" {
					continue
				}
				kind := "file"
				if e.IsDir() {
					kind = "dir"
				}
				fmt.Fprintf(&b, "%s\t%s\n", kind, e.Name())
				n++
				if n >= maxListEntries {
					b.WriteString("… truncated\n")
					break
				}
			}
			return agents.ToolResult{Content: strings.TrimSpace(b.String()), Meta: map[string]any{"path": args.Path, "count": n}}, nil
		},
	}
}

func readFileTool(root string) agents.RegisteredTool {
	return agents.RegisteredTool{
		Toolset: "workspace",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "read_file",
				Description: "Read a text file inside the approved workspace. Path is relative to the workspace root.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
					"required":   []string{"path"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *agents.RunContext, argsJSON string) (agents.ToolResult, error) {
			args, err := agents.ParseArgs[struct {
				Path string `json:"path"`
			}](argsJSON)
			if err != nil {
				return agents.ToolResult{}, err
			}
			path, err := ResolveWorkspacePath(root, args.Path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			info, err := os.Stat(path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if info.IsDir() {
				return agents.ToolResult{Content: "Error: path is a directory"}, nil
			}
			if info.Size() > maxReadBytes {
				return agents.ToolResult{Content: fmt.Sprintf("Error: file larger than %d bytes", maxReadBytes)}, nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if !utf8.Valid(raw) {
				return agents.ToolResult{Content: "Error: file is not valid UTF-8"}, nil
			}
			return agents.ToolResult{Content: string(raw), Meta: map[string]any{"path": args.Path, "bytes": len(raw)}}, nil
		},
	}
}

func searchFilesTool(root string) agents.RegisteredTool {
	return agents.RegisteredTool{
		Toolset: "workspace",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "search_files",
				Description: "Search file contents inside the workspace for a query string. Skips hidden and vendor directories.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
						"path":  map[string]any{"type": "string", "description": "Relative subdirectory to search"},
					},
					"required": []string{"query"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *agents.RunContext, argsJSON string) (agents.ToolResult, error) {
			args, err := agents.ParseArgs[struct {
				Query string `json:"query"`
				Path  string `json:"path"`
			}](argsJSON)
			if err != nil {
				return agents.ToolResult{}, err
			}
			q := strings.TrimSpace(args.Query)
			if q == "" {
				return agents.ToolResult{Content: "Error: query is required"}, nil
			}
			start, err := ResolveWorkspacePath(root, args.Path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			var hits []string
			_ = filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
				if err != nil || ctx.Err() != nil {
					return err
				}
				name := d.Name()
				if d.IsDir() {
					if name == ".git" || name == "node_modules" || name == "dist" || name == "vendor" || name == ".next" {
						return filepath.SkipDir
					}
					return nil
				}
				if len(hits) >= 20 {
					return io.EOF
				}
				info, err := d.Info()
				if err != nil || info.Size() > maxReadBytes {
					return nil
				}
				raw, err := os.ReadFile(path)
				if err != nil || !utf8.Valid(raw) {
					return nil
				}
				if !strings.Contains(string(raw), q) {
					return nil
				}
				rel, _ := filepath.Rel(root, path)
				hits = append(hits, rel)
				return nil
			})
			if len(hits) == 0 {
				return agents.ToolResult{Content: "No matches."}, nil
			}
			raw, _ := json.Marshal(hits)
			return agents.ToolResult{Content: string(raw), Meta: map[string]any{"hits": hits}}, nil
		},
	}
}

func writeFileTool(root string) agents.RegisteredTool {
	return agents.RegisteredTool{
		Toolset: "workspace",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "write_file",
				Description: "Create or overwrite a text file inside the workspace. Path is relative. Will not write outside the workspace.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string"},
						"content": map[string]any{"type": "string"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *agents.RunContext, argsJSON string) (agents.ToolResult, error) {
			args, err := agents.ParseArgs[struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}](argsJSON)
			if err != nil {
				return agents.ToolResult{}, err
			}
			if len(args.Content) > maxWriteBytes {
				return agents.ToolResult{Content: "Error: content too large"}, nil
			}
			path, err := ResolveWorkspacePath(root, args.Path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			return agents.ToolResult{Content: "Wrote " + args.Path, Meta: map[string]any{"path": args.Path, "bytes": len(args.Content)}}, nil
		},
	}
}

func applyPatchTool(root string) agents.RegisteredTool {
	return agents.RegisteredTool{
		Toolset: "workspace",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "apply_patch",
				Description: "Replace a unique old_string with new_string in a workspace file.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":       map[string]any{"type": "string"},
						"old_string": map[string]any{"type": "string"},
						"new_string": map[string]any{"type": "string"},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *agents.RunContext, argsJSON string) (agents.ToolResult, error) {
			args, err := agents.ParseArgs[struct {
				Path      string `json:"path"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			}](argsJSON)
			if err != nil {
				return agents.ToolResult{}, err
			}
			path, err := ResolveWorkspacePath(root, args.Path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			content := string(raw)
			if strings.Count(content, args.OldString) != 1 {
				return agents.ToolResult{Content: "Error: old_string must match exactly once"}, nil
			}
			next := strings.Replace(content, args.OldString, args.NewString, 1)
			if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			return agents.ToolResult{Content: "Patched " + args.Path, Meta: map[string]any{"path": args.Path}}, nil
		},
	}
}

func deleteFileTool(root string) agents.RegisteredTool {
	return agents.RegisteredTool{
		Toolset: "workspace",
		Definition: providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionSchema{
				Name:        "delete_file",
				Description: "Delete a file inside the workspace. Directories are refused.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
					"required":   []string{"path"},
				},
			},
		},
		Handle: func(ctx context.Context, runCtx *agents.RunContext, argsJSON string) (agents.ToolResult, error) {
			args, err := agents.ParseArgs[struct {
				Path string `json:"path"`
			}](argsJSON)
			if err != nil {
				return agents.ToolResult{}, err
			}
			path, err := ResolveWorkspacePath(root, args.Path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			info, err := os.Stat(path)
			if err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			if info.IsDir() {
				return agents.ToolResult{Content: "Error: refusing to delete a directory"}, nil
			}
			if err := os.Remove(path); err != nil {
				return agents.ToolResult{Content: "Error: " + err.Error()}, nil
			}
			return agents.ToolResult{Content: "Deleted " + args.Path, Meta: map[string]any{"path": args.Path}}, nil
		},
	}
}
