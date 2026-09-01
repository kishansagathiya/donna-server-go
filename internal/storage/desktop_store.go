package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const DeviceOfflineAfter = 45 * time.Second

type DesktopDevice struct {
	ID             string          `json:"id"`
	UserID         string          `json:"user_id"`
	PublicDeviceID string          `json:"public_device_id"`
	Name           string          `json:"name"`
	Platform       string          `json:"platform"`
	Architecture   string          `json:"architecture"`
	AppVersion     string          `json:"app_version"`
	Capabilities   json.RawMessage `json:"capabilities"`
	LastSeenAt     *string         `json:"last_seen_at,omitempty"`
	IsDefault      bool            `json:"is_default"`
	RevokedAt      *string         `json:"revoked_at,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

func (d DesktopDevice) Revoked() bool {
	return d.RevokedAt != nil && strings.TrimSpace(*d.RevokedAt) != ""
}

func (d DesktopDevice) Online(now time.Time) bool {
	if d.Revoked() || d.LastSeenAt == nil {
		return false
	}
	ts, err := time.Parse(time.RFC3339Nano, *d.LastSeenAt)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, *d.LastSeenAt)
	}
	if err != nil {
		return false
	}
	return now.Sub(ts) <= DeviceOfflineAfter
}

type DesktopWorkspace struct {
	ID           string          `json:"id"`
	UserID       string          `json:"user_id"`
	DeviceID     string          `json:"device_id"`
	Name         string          `json:"name"`
	Capabilities json.RawMessage `json:"capabilities"`
	LastSeenAt   *string         `json:"last_seen_at,omitempty"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

type RegisterDeviceInput struct {
	PublicDeviceID string
	Name           string
	Platform       string
	Architecture   string
	AppVersion     string
	Capabilities   map[string]any
}

type DesktopStore struct {
	DB      *Supabase
	Enabled bool
}

func (s *DesktopStore) selectDeviceColumns() string {
	return "id,user_id,public_device_id,name,platform,architecture,app_version,capabilities,last_seen_at,is_default,revoked_at,created_at,updated_at"
}

func (s *DesktopStore) selectWorkspaceColumns() string {
	return "id,user_id,device_id,name,capabilities,last_seen_at,created_at,updated_at"
}

func (s *DesktopStore) RegisterDevice(ctx context.Context, userID string, in RegisterDeviceInput) (DesktopDevice, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return DesktopDevice{}, fmt.Errorf("desktop_disabled")
	}
	publicID := strings.TrimSpace(in.PublicDeviceID)
	if publicID == "" {
		return DesktopDevice{}, fmt.Errorf("public_device_id_required")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Mac"
	}
	platform := strings.TrimSpace(in.Platform)
	if platform == "" {
		platform = "macos"
	}
	if platform != "macos" {
		return DesktopDevice{}, fmt.Errorf("unsupported_platform")
	}
	arch := strings.TrimSpace(in.Architecture)
	if arch == "" {
		arch = "arm64"
	}
	caps := in.Capabilities
	if caps == nil {
		caps = map[string]any{}
	}
	rawCaps, _ := json.Marshal(caps)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	existing, err := s.GetByPublicID(ctx, userID, publicID)
	if err == nil {
		if existing.Revoked() {
			return DesktopDevice{}, fmt.Errorf("device_revoked")
		}
		patch := map[string]any{
			"name":         name,
			"platform":     platform,
			"architecture": arch,
			"app_version":  strings.TrimSpace(in.AppVersion),
			"capabilities": json.RawMessage(rawCaps),
			"last_seen_at": now,
			"updated_at":   now,
		}
		return s.patchDevice(ctx, userID, existing.ID, patch)
	}
	if !strings.Contains(err.Error(), "not_found") {
		return DesktopDevice{}, err
	}

	others, err := s.ListDevices(ctx, userID, false)
	if err != nil {
		return DesktopDevice{}, err
	}
	isDefault := true
	for _, d := range others {
		if !d.Revoked() {
			isDefault = false
			break
		}
	}

	body := map[string]any{
		"user_id":          userID,
		"public_device_id": publicID,
		"name":             name,
		"platform":         platform,
		"architecture":     arch,
		"app_version":      strings.TrimSpace(in.AppVersion),
		"capabilities":     json.RawMessage(rawCaps),
		"last_seen_at":     now,
		"is_default":       isDefault,
	}
	var rows []DesktopDevice
	if err := s.DB.Insert(ctx, "desktop_devices", body, &rows); err != nil {
		return DesktopDevice{}, err
	}
	if len(rows) == 0 {
		return DesktopDevice{}, fmt.Errorf("device_create_empty")
	}
	return rows[0], nil
}

func (s *DesktopStore) GetDevice(ctx context.Context, userID, deviceID string) (DesktopDevice, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return DesktopDevice{}, fmt.Errorf("desktop_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectDeviceColumns())
	q.Set("id", "eq."+deviceID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []DesktopDevice
	if err := s.DB.Get(ctx, "desktop_devices", q, &rows); err != nil {
		return DesktopDevice{}, err
	}
	if len(rows) == 0 {
		return DesktopDevice{}, fmt.Errorf("device_not_found")
	}
	return rows[0], nil
}

func (s *DesktopStore) GetByPublicID(ctx context.Context, userID, publicID string) (DesktopDevice, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return DesktopDevice{}, fmt.Errorf("desktop_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectDeviceColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("public_device_id", "eq."+publicID)
	q.Set("limit", "1")
	var rows []DesktopDevice
	if err := s.DB.Get(ctx, "desktop_devices", q, &rows); err != nil {
		return DesktopDevice{}, err
	}
	if len(rows) == 0 {
		return DesktopDevice{}, fmt.Errorf("device_not_found")
	}
	return rows[0], nil
}

func (s *DesktopStore) ListDevices(ctx context.Context, userID string, includeRevoked bool) ([]DesktopDevice, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("desktop_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectDeviceColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "is_default.desc,last_seen_at.desc.nullslast")
	var rows []DesktopDevice
	if err := s.DB.Get(ctx, "desktop_devices", q, &rows); err != nil {
		return nil, err
	}
	if !includeRevoked {
		filtered := make([]DesktopDevice, 0, len(rows))
		for _, d := range rows {
			if !d.Revoked() {
				filtered = append(filtered, d)
			}
		}
		rows = filtered
	}
	if rows == nil {
		rows = []DesktopDevice{}
	}
	return rows, nil
}

func (s *DesktopStore) DefaultDevice(ctx context.Context, userID string) (DesktopDevice, error) {
	devices, err := s.ListDevices(ctx, userID, false)
	if err != nil {
		return DesktopDevice{}, err
	}
	for _, d := range devices {
		if d.IsDefault && !d.Revoked() {
			return d, nil
		}
	}
	if len(devices) > 0 {
		return devices[0], nil
	}
	return DesktopDevice{}, fmt.Errorf("device_not_found")
}

func (s *DesktopStore) HeartbeatDevice(ctx context.Context, userID, deviceID, appVersion string) (DesktopDevice, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	patch := map[string]any{
		"last_seen_at": now,
		"updated_at":   now,
	}
	if v := strings.TrimSpace(appVersion); v != "" {
		patch["app_version"] = v
	}
	device, err := s.patchDevice(ctx, userID, deviceID, patch)
	if err != nil {
		return DesktopDevice{}, err
	}
	if device.Revoked() {
		return DesktopDevice{}, fmt.Errorf("device_revoked")
	}
	return device, nil
}

func (s *DesktopStore) RevokeDevice(ctx context.Context, userID, deviceID string) (DesktopDevice, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return s.patchDevice(ctx, userID, deviceID, map[string]any{
		"revoked_at": now,
		"is_default": false,
		"updated_at": now,
	})
}

func (s *DesktopStore) patchDevice(ctx context.Context, userID, deviceID string, patch map[string]any) (DesktopDevice, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return DesktopDevice{}, fmt.Errorf("desktop_disabled")
	}
	q := url.Values{}
	q.Set("id", "eq."+deviceID)
	q.Set("user_id", "eq."+userID)
	var rows []DesktopDevice
	if err := s.DB.PatchReturning(ctx, "desktop_devices", q, patch, &rows); err != nil {
		return DesktopDevice{}, err
	}
	if len(rows) == 0 {
		return DesktopDevice{}, fmt.Errorf("device_not_found")
	}
	return rows[0], nil
}

func (s *DesktopStore) ListWorkspaces(ctx context.Context, userID string, deviceID string) ([]DesktopWorkspace, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("desktop_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectWorkspaceColumns())
	q.Set("user_id", "eq."+userID)
	if strings.TrimSpace(deviceID) != "" {
		q.Set("device_id", "eq."+deviceID)
	}
	q.Set("order", "name.asc")
	var rows []DesktopWorkspace
	if err := s.DB.Get(ctx, "desktop_workspaces", q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []DesktopWorkspace{}
	}
	return rows, nil
}

func (s *DesktopStore) GetWorkspace(ctx context.Context, userID, workspaceID string) (DesktopWorkspace, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return DesktopWorkspace{}, fmt.Errorf("desktop_disabled")
	}
	q := url.Values{}
	q.Set("select", s.selectWorkspaceColumns())
	q.Set("id", "eq."+workspaceID)
	q.Set("user_id", "eq."+userID)
	q.Set("limit", "1")
	var rows []DesktopWorkspace
	if err := s.DB.Get(ctx, "desktop_workspaces", q, &rows); err != nil {
		return DesktopWorkspace{}, err
	}
	if len(rows) == 0 {
		return DesktopWorkspace{}, fmt.Errorf("workspace_not_found")
	}
	return rows[0], nil
}

type WorkspaceSync struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Capabilities map[string]any `json:"capabilities"`
}

// ReplaceDeviceWorkspaces upserts the advertised workspace catalog for a device.
// IDs are client-generated UUIDs; absolute paths are never accepted.
func (s *DesktopStore) ReplaceDeviceWorkspaces(ctx context.Context, userID, deviceID string, workspaces []WorkspaceSync) ([]DesktopWorkspace, error) {
	if s == nil || !s.Enabled || s.DB == nil {
		return nil, fmt.Errorf("desktop_disabled")
	}
	if _, err := s.GetDevice(ctx, userID, deviceID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	keep := map[string]struct{}{}
	out := make([]DesktopWorkspace, 0, len(workspaces))
	for _, w := range workspaces {
		id := strings.TrimSpace(w.ID)
		name := strings.TrimSpace(w.Name)
		if id == "" || name == "" {
			return nil, fmt.Errorf("workspace_id_and_name_required")
		}
		keep[id] = struct{}{}
		caps := w.Capabilities
		if caps == nil {
			caps = map[string]any{}
		}
		rawCaps, _ := json.Marshal(caps)
		body := map[string]any{
			"id":           id,
			"user_id":      userID,
			"device_id":    deviceID,
			"name":         name,
			"capabilities": json.RawMessage(rawCaps),
			"last_seen_at": now,
			"updated_at":   now,
		}
		var rows []DesktopWorkspace
		if err := s.DB.Upsert(ctx, "desktop_workspaces", "id", body, &rows); err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			out = append(out, rows[0])
		}
	}

	existing, err := s.ListWorkspaces(ctx, userID, deviceID)
	if err != nil {
		return nil, err
	}
	for _, w := range existing {
		if _, ok := keep[w.ID]; ok {
			continue
		}
		q := url.Values{}
		q.Set("id", "eq."+w.ID)
		q.Set("user_id", "eq."+userID)
		q.Set("device_id", "eq."+deviceID)
		_ = s.DB.Delete(ctx, "desktop_workspaces", q)
	}
	if out == nil {
		out = []DesktopWorkspace{}
	}
	return out, nil
}
