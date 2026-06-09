package knowledge

import "sort"

// Mirrors donna-server/src/knowledge/ingest/mime.ts EXTENSION_MIME values.
var extensionMIME = map[string]string{
	".txt":      "text/plain",
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".pdf":      "application/pdf",
	".html":     "text/html",
	".htm":      "text/html",
	".json":     "application/json",
	".csv":      "text/csv",
	".docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".jpg":      "image/jpeg",
	".jpeg":     "image/jpeg",
	".png":      "image/png",
	".webp":     "image/webp",
	".heic":     "image/heic",
	".gif":       "image/gif",
	".m4a":      "audio/mp4",
	".mp3":      "audio/mpeg",
	".wav":      "audio/wav",
	".ts":       "text/plain",
	".tsx":      "text/plain",
	".js":       "text/plain",
	".jsx":      "text/plain",
	".py":       "text/plain",
	".go":       "text/plain",
	".rs":       "text/plain",
	".java":     "text/plain",
	".swift":    "text/plain",
	".rb":       "text/plain",
	".sql":      "text/plain",
	".yaml":     "text/plain",
	".yml":      "text/plain",
	".xml":      "text/xml",
}

var extractors = []struct {
	Name     string `json:"name"`
	Priority int    `json:"priority"`
}{
	{Name: "plain_text", Priority: 10},
	{Name: "html_strip", Priority: 20},
	{Name: "structured_data", Priority: 25},
	{Name: "pdf_parse", Priority: 30},
	{Name: "mammoth_docx", Priority: 35},
	{Name: "vision_llm", Priority: 40},
	{Name: "stt_transcribe", Priority: 50},
}

type FormatsResponse struct {
	MimeTypes  []string `json:"mime_types"`
	Extractors []struct {
		Name     string `json:"name"`
		Priority int    `json:"priority"`
	} `json:"extractors"`
}

func SupportedFormats() FormatsResponse {
	seen := make(map[string]struct{})
	var mimeTypes []string
	for _, mime := range extensionMIME {
		if _, ok := seen[mime]; ok {
			continue
		}
		seen[mime] = struct{}{}
		mimeTypes = append(mimeTypes, mime)
	}
	sort.Strings(mimeTypes)

	resp := FormatsResponse{
		MimeTypes: mimeTypes,
	}
	for _, e := range extractors {
		resp.Extractors = append(resp.Extractors, struct {
			Name     string `json:"name"`
			Priority int    `json:"priority"`
		}{Name: e.Name, Priority: e.Priority})
	}
	return resp
}
