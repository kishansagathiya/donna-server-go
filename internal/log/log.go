package log

import (
	"fmt"
	"log"
)

const prefix = "[donna-server-go]"

func Print(message string, data map[string]any) {
	if len(data) > 0 {
		log.Printf("%s %s %v", prefix, message, data)
		return
	}
	log.Printf("%s %s", prefix, message)
}

func Warn(message string, data map[string]any) {
	if len(data) > 0 {
		log.Printf("%s WARN %s %v", prefix, message, data)
		return
	}
	log.Printf("%s WARN %s", prefix, message)
}

func ShortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func FormatData(key string, value any) map[string]any {
	return map[string]any{key: value}
}

func FormatFields(fields map[string]any) string {
	return fmt.Sprint(fields)
}
