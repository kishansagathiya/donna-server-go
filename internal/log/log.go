package log

import (
	"fmt"
	"log"
	"sync/atomic"
)

const prefix = "[donna-server-go]"

// errorHook, when set, is invoked by Error with the raw message and fields.
// It must never block (main wires it to async GitHub issue reporting).
var errorHook atomic.Value // func(message string, fields map[string]any)

func SetErrorHook(hook func(message string, fields map[string]any)) {
	errorHook.Store(hook)
}

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

// Error logs actionable failures and forwards them to the error hook (GitHub
// issue reporting). Use for unexpected failures worth an issue; keep Warn for
// transient/benign conditions.
func Error(message string, data map[string]any) {
	if len(data) > 0 {
		log.Printf("%s ERROR %s %v", prefix, message, data)
	} else {
		log.Printf("%s ERROR %s", prefix, message)
	}
	if hook, ok := errorHook.Load().(func(string, map[string]any)); ok && hook != nil {
		hook(message, data)
	}
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
