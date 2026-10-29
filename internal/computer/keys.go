package computer

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type KeyChord struct {
	Key     string
	Control bool
	Alt     bool
	Shift   bool
	Meta    bool
}

func ParseKey(raw string) (KeyChord, error) {
	var chord KeyChord
	parts := strings.Split(raw, "+")
	if len(parts) > 5 {
		return chord, errors.New("invalid_key_chord")
	}
	chord.Key = parts[len(parts)-1]
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "Control":
			if chord.Control {
				return chord, errors.New("duplicate_modifier")
			}
			chord.Control = true
		case "Alt":
			if chord.Alt {
				return chord, errors.New("duplicate_modifier")
			}
			chord.Alt = true
		case "Shift":
			if chord.Shift {
				return chord, errors.New("duplicate_modifier")
			}
			chord.Shift = true
		case "Meta":
			if chord.Meta {
				return chord, errors.New("duplicate_modifier")
			}
			chord.Meta = true
		default:
			return chord, errors.New("invalid_modifier")
		}
	}
	valid := false
	for _, key := range []string{"Enter", "Tab", "Escape", "Backspace", "Delete", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "PageUp", "PageDown", "Space"} {
		if chord.Key == key {
			valid = true
		}
	}
	if utf8.RuneCountInString(chord.Key) == 1 && len(chord.Key) == 1 && chord.Key[0] >= 32 && chord.Key[0] <= 126 {
		valid = true
	}
	if !valid {
		return chord, errors.New("unsupported_key")
	}
	if chord.Meta && (chord.Key == "Tab" || chord.Key == "Space" || chord.Alt && chord.Key == "Escape" || chord.Control && strings.EqualFold(chord.Key, "q")) || chord.Alt && (chord.Key == "Tab" || chord.Key == "Escape") || chord.Control && chord.Alt && chord.Key == "Delete" || chord.Control && chord.Key == "Escape" {
		return chord, errors.New("system_shortcut_not_allowed")
	}
	return chord, nil
}
