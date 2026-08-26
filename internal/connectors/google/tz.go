package google

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // ensure IANA zones work in Alpine/scratch containers
)

// fixedZones covers every timezone offered in Donna Profile UIs.
// Used when the runtime zoneinfo database is missing (common on Alpine).
var fixedZones = map[string]*time.Location{
	"Asia/Kolkata":        time.FixedZone("Asia/Kolkata", 5*3600+30*60),
	"Asia/Calcutta":       time.FixedZone("Asia/Calcutta", 5*3600+30*60),
	"Asia/Dubai":          time.FixedZone("Asia/Dubai", 4*3600),
	"Asia/Singapore":      time.FixedZone("Asia/Singapore", 8*3600),
	"Asia/Tokyo":          time.FixedZone("Asia/Tokyo", 9*3600),
	"Europe/London":       time.FixedZone("Europe/London", 0),
	"Europe/Paris":        time.FixedZone("Europe/Paris", 1*3600),
	"Europe/Berlin":       time.FixedZone("Europe/Berlin", 1*3600),
	"America/New_York":    time.FixedZone("America/New_York", -5*3600),
	"America/Chicago":     time.FixedZone("America/Chicago", -6*3600),
	"America/Denver":      time.FixedZone("America/Denver", -7*3600),
	"America/Los_Angeles": time.FixedZone("America/Los_Angeles", -8*3600),
	"America/Sao_Paulo":   time.FixedZone("America/Sao_Paulo", -3*3600),
	"Australia/Sydney":    time.FixedZone("Australia/Sydney", 10*3600),
	"Pacific/Auckland":    time.FixedZone("Pacific/Auckland", 12*3600),
}

// LoadTZ resolves an IANA timezone even when the OS zoneinfo DB is absent.
func LoadTZ(name string) (*time.Location, error) {
	return loadLocation(name)
}

// loadLocation resolves an IANA timezone even when the OS zoneinfo DB is absent.
func loadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("invalid_timezone:empty")
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc, nil
	}
	if loc, ok := fixedZones[name]; ok {
		return loc, nil
	}
	// Last resort: accept numeric offsets like UTC+05:30 / GMT+5:30.
	if loc, ok := parseFixedOffsetName(name); ok {
		return loc, nil
	}
	return nil, fmt.Errorf("invalid_timezone:%s", name)
}

func parseFixedOffsetName(name string) (*time.Location, bool) {
	upper := strings.ToUpper(strings.TrimSpace(name))
	upper = strings.ReplaceAll(upper, " ", "")
	for _, prefix := range []string{"UTC", "GMT"} {
		if !strings.HasPrefix(upper, prefix) {
			continue
		}
		rest := upper[len(prefix):]
		if rest == "" {
			return time.UTC, true
		}
		sign := 1
		switch rest[0] {
		case '+':
			rest = rest[1:]
		case '-':
			sign = -1
			rest = rest[1:]
		default:
			return nil, false
		}
		parts := strings.Split(rest, ":")
		if len(parts) == 0 || parts[0] == "" {
			return nil, false
		}
		hours := 0
		mins := 0
		if _, err := fmt.Sscanf(parts[0], "%d", &hours); err != nil {
			return nil, false
		}
		if len(parts) > 1 {
			if _, err := fmt.Sscanf(parts[1], "%d", &mins); err != nil {
				return nil, false
			}
		}
		offset := sign * (hours*3600 + mins*60)
		return time.FixedZone(name, offset), true
	}
	return nil, false
}
