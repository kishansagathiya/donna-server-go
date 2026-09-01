package desktop

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kishansagathiya/donna/donna-server-go/internal/agents"
	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const deviceHeader = "X-Donna-Device-Id"

type Handler struct {
	Devices *storage.DesktopStore
	Agents  *storage.AgentsStore
	Hub     *Hub
	LLM     agents.Completer
}

func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.Route("/desktop", func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/devices/register", h.RegisterDevice)
		r.Get("/devices", h.ListDevices)
		r.Post("/devices/{id}/heartbeat", h.HeartbeatDevice)
		r.Post("/devices/{id}/revoke", h.RevokeDevice)
		r.Get("/workspaces", h.ListWorkspaces)
		r.Put("/devices/{id}/workspaces", h.ReplaceWorkspaces)
		r.Get("/runner", h.Runner)
		r.Post("/runs/{id}/claim", h.ClaimRun)
		r.Post("/runs/{id}/heartbeat", h.HeartbeatRun)
		r.Post("/runs/{id}/steps/batch", h.BatchSteps)
		r.Patch("/runs/{id}/state", h.PatchRunState)
		r.Post("/model/complete", h.Complete)
	})
}

func (h *Handler) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDOrUnauthorized(w, r)
	if !ok {
		return
	}
	if h.Devices == nil || !h.Devices.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "desktop_disabled"})
		return
	}
	var body struct {
		PublicDeviceID string         `json:"public_device_id"`
		Name           string         `json:"name"`
		Platform       string         `json:"platform"`
		Architecture   string         `json:"architecture"`
		AppVersion     string         `json:"app_version"`
		Capabilities   map[string]any `json:"capabilities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	device, err := h.Devices.RegisterDevice(r.Context(), userID, storage.RegisterDeviceInput{
		PublicDeviceID: body.PublicDeviceID,
		Name:           body.Name,
		Platform:       body.Platform,
		Architecture:   body.Architecture,
		AppVersion:     body.AppVersion,
		Capabilities:   body.Capabilities,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "revoked") {
			status = http.StatusForbidden
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (h *Handler) ListDevices(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDOrUnauthorized(w, r)
	if !ok {
		return
	}
	if h.Devices == nil || !h.Devices.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "desktop_disabled"})
		return
	}
	includeRevoked := r.URL.Query().Get("include_revoked") == "1"
	devices, err := h.Devices.ListDevices(r.Context(), userID, includeRevoked)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (h *Handler) HeartbeatDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDOrUnauthorized(w, r)
	if !ok {
		return
	}
	var body struct {
		AppVersion string `json:"app_version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	device, err := h.Devices.HeartbeatDevice(r.Context(), userID, chi.URLParam(r, "id"), body.AppVersion)
	if err != nil {
		writeJSON(w, statusForDeviceErr(err), map[string]string{"error": err.Error()})
		return
	}
	h.refreshQueuedWaiting(r, userID, device)
	writeJSON(w, http.StatusOK, device)
}

func (h *Handler) RevokeDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDOrUnauthorized(w, r)
	if !ok {
		return
	}
	deviceID := chi.URLParam(r, "id")
	device, err := h.Devices.RevokeDevice(r.Context(), userID, deviceID)
	if err != nil {
		writeJSON(w, statusForDeviceErr(err), map[string]string{"error": err.Error()})
		return
	}
	if h.Hub != nil {
		h.Hub.Publish(userID, deviceID, "device.revoked", map[string]any{"device_id": deviceID})
	}
	writeJSON(w, http.StatusOK, device)
}

func (h *Handler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDOrUnauthorized(w, r)
	if !ok {
		return
	}
	workspaces, err := h.Devices.ListWorkspaces(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("device_id")))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, workspaces)
}

func (h *Handler) ReplaceWorkspaces(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDOrUnauthorized(w, r)
	if !ok {
		return
	}
	device, err := h.requireDevice(r, userID, chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, statusForDeviceErr(err), map[string]string{"error": err.Error()})
		return
	}
	var body struct {
		Workspaces []storage.WorkspaceSync `json:"workspaces"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	rows, err := h.Devices.ReplaceDeviceWorkspaces(r.Context(), userID, device.ID, body.Workspaces)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.markMissingWorkspaces(r, userID, device.ID, rows)
	writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) ClaimRun(w http.ResponseWriter, r *http.Request) {
	userID, device, ok := h.deviceContext(w, r)
	if !ok {
		return
	}
	var body struct {
		WorkerID string `json:"worker_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	workerID := strings.TrimSpace(body.WorkerID)
	if workerID == "" {
		workerID = "desktop:" + device.ID
	}
	run, err := h.Agents.ClaimForDevice(r.Context(), userID, chi.URLParam(r, "id"), device.ID, workerID, agents.DefaultLease)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	_, _ = h.Agents.Patch(r.Context(), userID, run.ID, map[string]any{"waiting_reason": nil})
	run.WaitingReason = nil
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) HeartbeatRun(w http.ResponseWriter, r *http.Request) {
	userID, device, ok := h.deviceContext(w, r)
	if !ok {
		return
	}
	var body struct {
		WorkerID string `json:"worker_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	workerID := strings.TrimSpace(body.WorkerID)
	if workerID == "" {
		workerID = "desktop:" + device.ID
	}
	run, err := h.requireAssignedRun(r, userID, chi.URLParam(r, "id"), device.ID)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	run, err = h.Agents.Heartbeat(r.Context(), userID, run.ID, workerID, agents.DefaultLease)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) BatchSteps(w http.ResponseWriter, r *http.Request) {
	userID, device, ok := h.deviceContext(w, r)
	if !ok {
		return
	}
	run, err := h.requireAssignedRun(r, userID, chi.URLParam(r, "id"), device.ID)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	var body struct {
		Steps []struct {
			Seq     int            `json:"seq"`
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	out := make([]storage.AgentStep, 0, len(body.Steps))
	for _, st := range body.Steps {
		step, err := h.Agents.AppendStep(r.Context(), userID, run.ID, st.Seq, st.Kind, st.Payload)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out = append(out, step)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) PatchRunState(w http.ResponseWriter, r *http.Request) {
	userID, device, ok := h.deviceContext(w, r)
	if !ok {
		return
	}
	run, err := h.requireAssignedRun(r, userID, chi.URLParam(r, "id"), device.ID)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	var body struct {
		Status        string         `json:"status"`
		Result        map[string]any `json:"result"`
		Error         string         `json:"error"`
		Plan          []any          `json:"plan"`
		WaitingReason *string        `json:"waiting_reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	status := strings.TrimSpace(body.Status)
	switch status {
	case storage.AgentStatusSucceeded, storage.AgentStatusFailed, storage.AgentStatusCancelled, storage.AgentStatusExpired:
		run, err = h.Agents.Finish(r.Context(), userID, run.ID, status, body.Result, body.Error)
	case storage.AgentStatusWaitingForUser:
		run, err = h.Agents.WaitForUser(r.Context(), userID, run.ID, body.Result)
	case storage.AgentStatusQueued:
		run, err = h.Agents.Requeue(r.Context(), userID, run.ID)
	case "":
		patch := map[string]any{}
		if body.Plan != nil {
			raw, _ := json.Marshal(body.Plan)
			patch["plan"] = json.RawMessage(raw)
		}
		if body.WaitingReason != nil {
			patch["waiting_reason"] = *body.WaitingReason
		}
		if len(patch) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty_patch"})
			return
		}
		run, err = h.Agents.Patch(r.Context(), userID, run.ID, patch)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_status"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	userID, device, ok := h.deviceContext(w, r)
	if !ok {
		return
	}
	if h.LLM == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model_unavailable"})
		return
	}
	var body struct {
		RunID      string                    `json:"run_id"`
		Messages   []providers.ChatMessage   `json:"messages"`
		Tools      []providers.ToolDefinition `json:"tools"`
		ToolChoice any                       `json:"tool_choice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	run, err := h.requireAssignedRun(r, userID, body.RunID, device.ID)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if run.Status != storage.AgentStatusRunning {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "run_not_active"})
		return
	}
	meta, err := h.LLM.CompleteOnceWithOptions(r.Context(), body.Messages, providers.ChatCompletionOptions{
		Tools:      body.Tools,
		ToolChoice: body.ToolChoice,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "llm_error", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) refreshQueuedWaiting(r *http.Request, userID string, device storage.DesktopDevice) {
	if h.Agents == nil || !h.Agents.Enabled {
		return
	}
	runs, err := h.Agents.ListClaimableForDevice(r.Context(), userID, device.ID, 50)
	if err != nil {
		return
	}
	busy := false
	n, _ := h.Agents.CountRunningOnDevice(r.Context(), userID, device.ID)
	if n > 0 {
		busy = true
	}
	for i, run := range runs {
		if run.Status != storage.AgentStatusQueued {
			continue
		}
		var reason *string
		if run.WorkspaceID != nil {
			if _, err := h.Devices.GetWorkspace(r.Context(), userID, *run.WorkspaceID); err != nil {
				v := storage.WaitingWorkspaceUnavailable
				reason = &v
			}
		}
		if reason == nil && busy && i > 0 {
			v := storage.WaitingDeviceBusy
			reason = &v
		}
		patch := map[string]any{"waiting_reason": reason}
		_, _ = h.Agents.Patch(r.Context(), userID, run.ID, patch)
		if h.Hub != nil && reason == nil {
			h.Hub.Publish(userID, device.ID, "run.available", map[string]any{"run_id": run.ID})
		}
	}
}

func (h *Handler) markMissingWorkspaces(r *http.Request, userID, deviceID string, current []storage.DesktopWorkspace) {
	if h.Agents == nil {
		return
	}
	keep := map[string]struct{}{}
	for _, w := range current {
		keep[w.ID] = struct{}{}
	}
	runs, err := h.Agents.ListClaimableForDevice(r.Context(), userID, deviceID, 100)
	if err != nil {
		return
	}
	for _, run := range runs {
		if run.WorkspaceID == nil {
			continue
		}
		if _, ok := keep[*run.WorkspaceID]; ok {
			continue
		}
		reason := storage.WaitingWorkspaceUnavailable
		_, _ = h.Agents.Patch(r.Context(), userID, run.ID, map[string]any{"waiting_reason": reason})
	}
}

func (h *Handler) deviceContext(w http.ResponseWriter, r *http.Request) (string, storage.DesktopDevice, bool) {
	userID, ok := userIDOrUnauthorized(w, r)
	if !ok {
		return "", storage.DesktopDevice{}, false
	}
	deviceID := strings.TrimSpace(r.Header.Get(deviceHeader))
	if deviceID == "" {
		deviceID = strings.TrimSpace(r.URL.Query().Get("device_id"))
	}
	device, err := h.requireDevice(r, userID, deviceID)
	if err != nil {
		writeJSON(w, statusForDeviceErr(err), map[string]string{"error": err.Error()})
		return "", storage.DesktopDevice{}, false
	}
	return userID, device, true
}

func (h *Handler) requireDevice(r *http.Request, userID, deviceID string) (storage.DesktopDevice, error) {
	if h.Devices == nil || !h.Devices.Enabled {
		return storage.DesktopDevice{}, errDesktopDisabled
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return storage.DesktopDevice{}, errDeviceRequired
	}
	device, err := h.Devices.GetDevice(r.Context(), userID, deviceID)
	if err != nil {
		return storage.DesktopDevice{}, err
	}
	if device.Revoked() {
		return storage.DesktopDevice{}, errDeviceRevoked
	}
	return device, nil
}

func (h *Handler) requireAssignedRun(r *http.Request, userID, runID, deviceID string) (storage.AgentRun, error) {
	if h.Agents == nil || !h.Agents.Enabled {
		return storage.AgentRun{}, errAgentsDisabled
	}
	run, err := h.Agents.Get(r.Context(), userID, strings.TrimSpace(runID))
	if err != nil {
		return storage.AgentRun{}, err
	}
	if run.ExecutionTarget != storage.ExecutionTargetLocal {
		return storage.AgentRun{}, errNotLocalRun
	}
	if run.AssignedDeviceID == nil || *run.AssignedDeviceID != deviceID {
		return storage.AgentRun{}, errWrongDevice
	}
	return run, nil
}

func userIDOrUnauthorized(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return "", false
	}
	return userID, true
}

func statusForDeviceErr(err error) int {
	if err == nil {
		return http.StatusOK
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "revoked"):
		return http.StatusForbidden
	case strings.Contains(s, "not_found"):
		return http.StatusNotFound
	case strings.Contains(s, "disabled"):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var (
	errDesktopDisabled = errString("desktop_disabled")
	errDeviceRequired  = errString("device_required")
	errDeviceRevoked   = errString("device_revoked")
	errAgentsDisabled  = errString("agents_disabled")
	errNotLocalRun     = errString("not_local_run")
	errWrongDevice     = errString("run_not_assigned_to_device")
)

type errString string

func (e errString) Error() string { return string(e) }
