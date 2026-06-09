package ingest

import (
	"strings"
	"unicode/utf8"
)

var extensionMIME = map[string]string{
	".txt": "text/plain", ".md": "text/markdown", ".markdown": "text/markdown",
	".pdf": "application/pdf", ".html": "text/html", ".htm": "text/html",
	".json": "application/json", ".csv": "text/csv",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
	".heic": "image/heic", ".gif": "image/gif",
	".m4a": "audio/mp4", ".mp3": "audio/mpeg", ".wav": "audio/wav",
	".ts": "text/plain", ".tsx": "text/plain", ".js": "text/plain", ".jsx": "text/plain",
	".py": "text/plain", ".go": "text/plain", ".rs": "text/plain", ".java": "text/plain",
	".swift": "text/plain", ".rb": "text/plain", ".sql": "text/plain",
	".yaml": "text/plain", ".yml": "text/plain", ".xml": "text/xml",
}

var codeExtensions = map[string]struct{}{
	".ts": {}, ".tsx": {}, ".js": {}, ".jsx": {}, ".py": {}, ".go": {}, ".rs": {},
	".java": {}, ".swift": {}, ".rb": {}, ".sql": {}, ".yaml": {}, ".yml": {}, ".xml": {},
}

func ResolveMime(buffer []byte, contentType, filename string) string {
	if magic := sniffFromMagic(buffer, filename); magic != "" {
		return magic
	}

	headerMime := ""
	if contentType != "" && contentType != "application/octet-stream" {
		headerMime = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	extMime := mimeFromExtension(filename)

	if headerMime != "" && headerMime != "application/octet-stream" {
		return headerMime
	}
	if extMime != "" {
		return extMime
	}
	if filename != "" {
		ext := "." + strings.ToLower(lastPart(filename, "."))
		if _, ok := codeExtensions[ext]; ok && isValidUTF8Text(buffer) {
			return "text/plain"
		}
	}
	if isValidUTF8Text(buffer) {
		return "text/plain"
	}
	return "application/octet-stream"
}

func AssetKindFromMime(mime string) AssetKind {
	if strings.HasPrefix(mime, "image/") {
		return AssetImage
	}
	if strings.HasPrefix(mime, "audio/") {
		return AssetAudio
	}
	if mime == "text/plain" || mime == "text/markdown" {
		return AssetText
	}
	return AssetDocument
}

func mimeFromExtension(filename string) string {
	if filename == "" {
		return ""
	}
	lower := strings.ToLower(filename)
	for ext, mime := range extensionMIME {
		if strings.HasSuffix(lower, ext) {
			return mime
		}
	}
	return ""
}

func sniffFromMagic(buffer []byte, filename string) string {
	if len(buffer) < 4 {
		return ""
	}
	if string(buffer[:4]) == "%PDF" {
		return "application/pdf"
	}
	if buffer[0] == 0xff && buffer[1] == 0xd8 && buffer[2] == 0xff {
		return "image/jpeg"
	}
	if buffer[0] == 0x89 && buffer[1] == 0x50 && buffer[2] == 0x4e && buffer[3] == 0x47 {
		return "image/png"
	}
	if len(buffer) >= 3 && string(buffer[:3]) == "GIF" {
		return "image/gif"
	}
	if len(buffer) >= 12 && string(buffer[:4]) == "RIFF" {
		form := string(buffer[8:12])
		if form == "WAVE" {
			return "audio/wav"
		}
		if form == "WEBP" {
			return "image/webp"
		}
	}
	if len(buffer) >= 3 && string(buffer[:3]) == "ID3" {
		return "audio/mpeg"
	}
	if buffer[0] == 0xff && (buffer[1]&0xe0) == 0xe0 {
		return "audio/mpeg"
	}
	if string(buffer[:4]) == "ftyp" {
		return "audio/mp4"
	}
	if buffer[0] == 0x50 && buffer[1] == 0x4b {
		if ext := mimeFromExtension(filename); ext != "" {
			return ext
		}
	}
	return ""
}

func isValidUTF8Text(buffer []byte) bool {
	sample := buffer
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	if !utf8.Valid(sample) {
		return false
	}
	text := string(sample)
	if strings.Contains(text, "\uFFFD") {
		return false
	}
	control := 0
	for _, r := range text {
		if r != '\n' && r != '\r' && r != '\t' && r < 32 {
			control++
		}
	}
	return control < len(text)/20
}

func lastPart(s, sep string) string {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[i+1:]
	}
	return s
}
