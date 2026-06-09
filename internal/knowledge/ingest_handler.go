package knowledge

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const maxUploadBytes = 15 * 1024 * 1024

type IngestHandler struct {
	KB    *storage.Knowledge
	Queue *Queue
}

func (h *IngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.KB == nil || !h.KB.Enabled {
		writeIngestJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "knowledge_disabled"})
		return
	}

	userID, ok := appauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeIngestJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}

	contentType := r.Header.Get("Content-Type")

	result, status, err := h.handleIngest(r, userID, contentType)
	if err != nil {
		if status == 0 {
			status = ingestErrorStatus(err)
		}
		writeIngestJSON(w, status, map[string]string{"error": "ingest_failed", "message": err.Error()})
		return
	}
	if status >= 400 {
		writeIngestJSON(w, status, result)
		return
	}
	writeIngestJSON(w, status, result)
}

func (h *IngestHandler) handleIngest(r *http.Request, userID, contentType string) (map[string]any, int, error) {
	var (
		extracted        ingest.ExtractedAsset
		storagePath      string
		originalFilename string
		sourceURL        *string
	)

	if strings.Contains(contentType, "application/json") {
		var body struct {
			URL   string `json:"url"`
			Text  string `json:"text"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, http.StatusUnprocessableEntity, err
		}
		if strings.TrimSpace(body.URL) != "" {
			u := strings.TrimSpace(body.URL)
			sourceURL = &u
			var err error
			extracted, err = ingest.ExtractURL(u)
			if err != nil {
				return nil, http.StatusUnprocessableEntity, err
			}
			originalFilename = u
		} else if strings.TrimSpace(body.Text) != "" {
			extracted = ingest.ExtractTextBody(body.Text, body.Title)
			originalFilename = coalesceStr(body.Title, "note.txt")
		} else {
			return map[string]any{"error": "invalid_body", "message": "Provide url or text"}, http.StatusUnprocessableEntity, nil
		}
	} else if strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
			return nil, http.StatusUnprocessableEntity, err
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return map[string]any{"error": "missing_file"}, http.StatusUnprocessableEntity, nil
		}
		defer file.Close()

		buffer, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
		if err != nil {
			return nil, http.StatusUnprocessableEntity, err
		}
		if len(buffer) > maxUploadBytes {
			return map[string]any{"error": "file_too_large"}, http.StatusRequestEntityTooLarge, nil
		}

		originalFilename = header.Filename
		extracted, err = ingest.DispatchFileExtraction(buffer, header.Header.Get("Content-Type"), header.Filename)
		if err != nil {
			return nil, ingestErrorStatus(err), err
		}

		storagePath, err = h.KB.UploadAssetFile(r.Context(), userID, header.Filename, buffer, extracted.MimeType)
		if err != nil {
			return nil, http.StatusUnprocessableEntity, err
		}
	} else {
		return map[string]any{"error": "unsupported_content_type"}, http.StatusUnsupportedMediaType, nil
	}

	sourceID, err := h.KB.InsertAssetSource(r.Context(), userID, extracted.Content, map[string]any{
		"asset_kind":        extracted.AssetKind,
		"mime_type":         extracted.MimeType,
		"original_filename": coalesceStr(originalFilename, extracted.Title),
		"storage_path":      nilIfEmptyStr(storagePath),
		"url":               sourceURL,
		"extractor":         extracted.Extractor,
		"title":             extracted.Title,
		"extracted_at":      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, http.StatusUnprocessableEntity, err
	}

	if h.Queue != nil {
		h.Queue.EnqueueAssetCompile(userID, sourceID)
	}

	h.KB.LogKnowledge("asset ingested", map[string]any{
		"userId":    shortID(userID),
		"sourceId":  sourceID,
		"assetKind": extracted.AssetKind,
		"extractor": extracted.Extractor,
	})

	title := coalesceStr(extracted.Title, originalFilename)
	return map[string]any{
		"source_id":  sourceID,
		"asset_kind": extracted.AssetKind,
		"title":      title,
		"status":     "queued",
	}, http.StatusOK, nil
}

func ingestErrorStatus(err error) int {
	msg := err.Error()
	if strings.HasPrefix(msg, "Unsupported") {
		return http.StatusUnsupportedMediaType
	}
	if strings.Contains(msg, "too large") {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusUnprocessableEntity
}

func writeIngestJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func coalesceStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func nilIfEmptyStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
