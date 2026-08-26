package notes

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/knowledge/ingest"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const maxNoteRequestBodyBytes = 32 << 20

type noteAttachmentInput struct {
	Kind       string `json:"kind"`
	Filename   string `json:"filename"`
	Mime       string `json:"mime"`
	DataBase64 string `json:"data_base64"`
}

func parseNoteImageAttachments(inputs []noteAttachmentInput) ([]storage.SaveNoteAttachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > storage.MaxNoteAttachments {
		return nil, fmt.Errorf("too many attachments (max %d)", storage.MaxNoteAttachments)
	}
	out := make([]storage.SaveNoteAttachment, 0, len(inputs))
	for i, att := range inputs {
		kind := strings.ToLower(strings.TrimSpace(att.Kind))
		if kind != "" && kind != "file" && kind != "image" {
			return nil, fmt.Errorf("attachment %d: notes only accept image files", i+1)
		}
		data, err := decodeNoteAttachmentBase64(att.DataBase64)
		if err != nil {
			return nil, fmt.Errorf("attachment %d: %w", i+1, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("attachment %d: empty file", i+1)
		}
		if len(data) > storage.MaxNoteAttachmentBytes {
			return nil, fmt.Errorf("attachment %d: file too large (max 15MB)", i+1)
		}
		filename := strings.TrimSpace(att.Filename)
		if filename == "" {
			filename = "photo.jpg"
		}
		mime := ingest.ResolveMime(data, att.Mime, filename)
		if !strings.HasPrefix(mime, "image/") {
			return nil, fmt.Errorf("attachment %d: only images can be attached to notes", i+1)
		}
		out = append(out, storage.SaveNoteAttachment{
			Filename: filename,
			Mime:     mime,
			Data:     data,
		})
	}
	return out, nil
}

func decodeNoteAttachmentBase64(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("file attachment requires data_base64")
	}
	if idx := strings.Index(raw, "base64,"); idx >= 0 {
		raw = raw[idx+len("base64,"):]
	}
	buf, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		buf, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 data")
		}
	}
	return buf, nil
}

func isRequestTooLarge(err error) bool {
	var maxBytes *http.MaxBytesError
	return errors.As(err, &maxBytes)
}
