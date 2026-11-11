package attachment

import "strings"

const (
	KindAudio Kind = "audio"
	KindVideo Kind = "video"
)

func detectMediaExtMime(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	default:
		return ""
	}
}

func classifyMediaAttachment(lowerMime, ext string) (Kind, string, bool) {
	switch lowerMime {
	case "audio/mp3":
		return KindAudio, "audio/mpeg", true
	case "audio/x-wav", "audio/wave":
		return KindAudio, "audio/wav", true
	case "audio/x-m4a", "audio/m4a":
		return KindAudio, "audio/mp4", true
	case "audio/x-flac":
		return KindAudio, "audio/flac", true
	case "audio/mpeg", "audio/wav", "audio/mp4", "audio/aac", "audio/ogg", "audio/opus", "audio/flac", "audio/webm":
		return KindAudio, lowerMime, true
	case "video/mp4", "video/webm", "video/quicktime", "video/x-matroska", "video/x-msvideo":
		return KindVideo, lowerMime, true
	}

	if strings.HasPrefix(lowerMime, "audio/") {
		return KindAudio, lowerMime, true
	}
	if strings.HasPrefix(lowerMime, "video/") {
		return KindVideo, lowerMime, true
	}

	if extMime := detectMediaExtMime(ext); extMime != "" {
		if strings.HasPrefix(extMime, "audio/") {
			return KindAudio, extMime, true
		}
		return KindVideo, extMime, true
	}
	return "", "", false
}

func mediaDefaultExtForMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return ".m4a"
	case "audio/aac":
		return ".aac"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/webm":
		return ".webm"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "video/x-matroska":
		return ".mkv"
	case "video/x-msvideo":
		return ".avi"
	default:
		return ""
	}
}
