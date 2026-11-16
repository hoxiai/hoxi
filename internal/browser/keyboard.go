package browser

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// keyAction accepts DOM key names and modifier chords, not literal chord text.
func keyAction(key string) (chromedp.Action, error) {
	if key == "" {
		return nil, errors.New("key required")
	}
	parts := strings.Split(key, "+")
	// A lone '+' is a printable key, including in chords such as Control++.
	if key == "+" {
		parts = []string{"+"}
	} else if strings.HasSuffix(key, "++") {
		parts = append(strings.Split(strings.TrimSuffix(key, "++"), "+"), "+")
	}
	var modifiers input.Modifier
	var held []rune
	for _, name := range parts[:len(parts)-1] {
		var flag input.Modifier
		var encoded string
		switch name {
		case "Control":
			flag, encoded = input.ModifierCtrl, kb.Control
		case "Meta":
			flag, encoded = input.ModifierMeta, kb.Meta
		case "Shift":
			flag, encoded = input.ModifierShift, kb.Shift
		case "Alt":
			flag, encoded = input.ModifierAlt, kb.Alt
		default:
			return nil, errors.New("unknown key modifier")
		}
		if modifiers&flag != 0 {
			return nil, errors.New("duplicate key modifier")
		}
		modifiers |= flag
		r, _ := utf8.DecodeRuneInString(encoded)
		held = append(held, r)
	}
	name := parts[len(parts)-1]
	var keyRune rune
	if utf8.RuneCountInString(name) == 1 {
		keyRune, _ = utf8.DecodeRuneInString(name)
	} else {
		found := false
		for r, mapping := range kb.Keys {
			if mapping.Key == name {
				keyRune, found = r, true
				break
			}
		}
		if !found {
			return nil, errors.New("unknown key name")
		}
	}
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Always release modifiers, including when a dispatch fails mid-chord.
		defer func() {
			for i := len(held) - 1; i >= 0; i-- {
				event := kb.Encode(held[i])
				_ = event[len(event)-1].WithModifiers(0).Do(ctx)
			}
		}()
		for _, r := range held {
			if err := kb.Encode(r)[0].WithModifiers(modifiers).Do(ctx); err != nil {
				return err
			}
		}
		for _, event := range kb.Encode(keyRune) {
			// Ctrl/Meta shortcuts must not also insert their printable character.
			if modifiers&(input.ModifierCtrl|input.ModifierMeta|input.ModifierAlt) != 0 {
				if event.Type == input.KeyChar {
					continue
				}
				event.Text, event.UnmodifiedText = "", ""
			}
			event.Modifiers |= modifiers
			// Headless Chrome on macOS does not resolve Cocoa editing shortcuts
			// from Meta key events alone. Supply the matching CDP edit command.
			shortcut := input.ModifierCtrl
			if runtime.GOOS == "darwin" {
				shortcut = input.ModifierMeta
			}
			if event.Type == input.KeyDown && modifiers == shortcut && (name == "a" || name == "A") {
				event.Commands = []string{"selectAll"}
			}
			if err := event.Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}), nil
}
