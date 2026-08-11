package chatgpt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	uploadURLTTL      = time.Hour
	maxImportBytes    = 512 * 1024 * 1024
	uploadContentType = "application/zip"
)

// Handler exposes REST routes for ChatGPT export import.
type Handler struct {
	Imports *storage.ChatGPTImports
	Blobs   storage.ImportBlobStore
	Jobs    *storage.BackgroundJobs
}

// RegisterRoutes mounts /imports/chatgpt* under auth.
func RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler, h *Handler) {
	r.With(authMiddleware).Post("/imports/chatgpt", h.Create)
	r.With(authMiddleware).Get("/imports/chatgpt", h.Latest)
	r.With(authMiddleware).Get("/imports/chatgpt/{id}", h.Get)
	r.With(authMiddleware).Post("/imports/chatgpt/{id}/start", h.Start)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Imports == nil || !h.Imports.Enabled || h.Blobs == nil || !h.Blobs.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "imports_disabled", "ChatGPT import is unavailable")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}

	importID := uuid.NewString()
	storagePath := fmt.Sprintf("%s/%s.zip", userID, importID)

	imp, err := h.Imports.Create(r.Context(), userID, importID, storagePath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}

	uploadURL, err := h.Blobs.PresignPut(r.Context(), storagePath, uploadContentType, uploadURLTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "upload_url_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":            imp.ID,
		"status":        imp.Status,
		"upload_url":    uploadURL,
		"upload_method": "PUT",
		"upload_headers": map[string]string{
			"Content-Type": uploadContentType,
		},
		"token":        "",
		"path":         storagePath,
		"bucket":       h.Blobs.Bucket(),
		"provider":     "railway_s3",
		"max_bytes":    maxImportBytes,
		"expires_in_s": int(uploadURLTTL.Seconds()),
	})
}

func (h *Handler) Latest(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Imports == nil || !h.Imports.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"import": nil})
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	imp, err := h.Imports.LatestForUser(r.Context(), userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if imp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"import": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"import": publicImport(*imp)})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Imports == nil || !h.Imports.Enabled {
		writeErr(w, http.StatusServiceUnavailable, "imports_disabled", "ChatGPT import is unavailable")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing_id", "import id required")
		return
	}
	imp, err := h.Imports.GetByID(r.Context(), userID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "import not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"import": publicImport(imp)})
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Imports == nil || !h.Imports.Enabled || h.Blobs == nil || !h.Blobs.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "imports_disabled", "ChatGPT import is unavailable")
		return
	}
	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeErr(w, http.StatusUnauthorized, "auth_required", "sign in required")
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing_id", "import id required")
		return
	}

	imp, err := h.Imports.GetByID(r.Context(), userID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "import not found")
		return
	}
	if imp.Status == storage.ChatGPTImportQueued || imp.Status == storage.ChatGPTImportRunning {
		writeJSON(w, http.StatusOK, map[string]any{"import": publicImport(imp)})
		return
	}
	if imp.Status == storage.ChatGPTImportCompleted {
		writeJSON(w, http.StatusOK, map[string]any{"import": publicImport(imp)})
		return
	}
	if imp.StoragePath == nil || *imp.StoragePath == "" {
		writeErr(w, http.StatusConflict, "missing_path", "import has no storage path")
		return
	}

	exists, err := h.Blobs.ObjectExists(r.Context(), *imp.StoragePath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage_check_failed", err.Error())
		return
	}
	if !exists {
		writeErr(w, http.StatusConflict, "upload_missing", "Upload the ChatGPT export ZIP before starting import")
		return
	}

	var body struct {
		Bytes *int64 `json:"bytes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Bytes != nil && *body.Bytes > maxImportBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "file_too_large", "Export ZIP must be 512MB or smaller")
		return
	}

	queued := storage.ChatGPTImportQueued
	patch := storage.ChatGPTImportPatch{
		Status:     &queued,
		ClearError: true,
	}
	if body.Bytes != nil {
		patch.Bytes = body.Bytes
	}
	imp, err = h.Imports.Patch(r.Context(), imp.ID, patch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "start_failed", err.Error())
		return
	}

	if h.Jobs == nil || !h.Jobs.Enabled {
		writeErr(w, http.StatusServiceUnavailable, "jobs_disabled", "Background processing is unavailable")
		return
	}
	key := fmt.Sprintf("chatgpt_export_import:%s:0", imp.ID)
	if _, err := h.Jobs.Enqueue(r.Context(), storage.EnqueueJobInput{
		UserID:     userID,
		JobType:    storage.JobTypeChatGPTExportImport,
		DedupeKey:  key,
		Payload:    map[string]any{"import_id": imp.ID, "cursor": 0},
		TargetKind: storage.TargetKindImport,
		TargetID:   imp.ID,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "enqueue_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"import": publicImport(imp)})
}

func publicImport(imp storage.ChatGPTImport) map[string]any {
	out := map[string]any{
		"id":                      imp.ID,
		"status":                  imp.Status,
		"conversations_total":     imp.ConversationsTotal,
		"conversations_processed": imp.ConversationsProcessed,
		"memories_imported":       imp.MemoriesImported,
		"cursor_index":            imp.CursorIndex,
		"created_at":              imp.CreatedAt,
		"updated_at":              imp.UpdatedAt,
	}
	if imp.Bytes != nil {
		out["bytes"] = *imp.Bytes
	}
	if imp.Error != nil {
		out["error"] = *imp.Error
	}
	if imp.StartedAt != nil {
		out["started_at"] = *imp.StartedAt
	}
	if imp.FinishedAt != nil {
		out["finished_at"] = *imp.FinishedAt
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": code, "message": message})
}
