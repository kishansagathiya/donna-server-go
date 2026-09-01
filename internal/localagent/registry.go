package localagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
)

func DesktopRegistry(mem agents.MemorySearcher, notes agents.NoteSearcher, browserURL, workspaceRoot string) *agents.Registry {
	reg := agents.DefaultToolsets(mem, notes, browserURL, nil, nil)
	if workspaceRoot != "" {
		for _, t := range workspaceTools(workspaceRoot) {
			reg.Register(t)
		}
		reg.Register(shellTool(workspaceRoot))
	}
	return reg
}

type SpoolItem struct {
	RunID   string         `json:"run_id"`
	Seq     int            `json:"seq"`
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}

type Spool struct {
	path string
	mu   sync.Mutex
}

func OpenSpool(dir string) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Spool{path: filepath.Join(dir, "step-spool.json")}, nil
}

func (s *Spool) Append(item SpoolItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, _ := s.readAll()
	items = append(items, item)
	return s.writeAll(items)
}

func (s *Spool) Take(runID string) []SpoolItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, _ := s.readAll()
	var keep, taken []SpoolItem
	for _, it := range items {
		if it.RunID == runID {
			taken = append(taken, it)
		} else {
			keep = append(keep, it)
		}
	}
	_ = s.writeAll(keep)
	return taken
}

func (s *Spool) readAll() ([]SpoolItem, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []SpoolItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Spool) writeAll(items []SpoolItem) error {
	if items == nil {
		items = []SpoolItem{}
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}
