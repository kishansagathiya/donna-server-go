package pipeline

type InteractionMode string

const (
	ModeTalk  InteractionMode = "talk"
	ModeNotes InteractionMode = "notes"
)

func ParseMode(raw string) InteractionMode {
	if raw == string(ModeNotes) || raw == "listen" {
		return ModeNotes
	}
	return ModeTalk
}

func (m InteractionMode) IsNotes() bool {
	return m == ModeNotes
}
