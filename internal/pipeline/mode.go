package pipeline

type InteractionMode string

const (
	ModeTalk   InteractionMode = "talk"
	ModeListen InteractionMode = "listen"
)

func ParseMode(raw string) InteractionMode {
	if raw == string(ModeListen) {
		return ModeListen
	}
	return ModeTalk
}

func (m InteractionMode) IsListen() bool {
	return m == ModeListen
}
