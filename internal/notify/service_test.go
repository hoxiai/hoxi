package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestShowNormalizesAndDelivers(t *testing.T) {
	svc := NewService()
	var got Request
	svc.deliver = func(req Request) error {
		got = req
		return nil
	}
	if err := svc.Show(Request{Title: "  Hoxi  ", Body: "  Turn completed  ", SessionID: " ses_1 "}); err != nil {
		t.Fatalf("show: %v", err)
	}
	if got.Title != "Hoxi" || got.Body != "Turn completed" || got.SessionID != "ses_1" {
		t.Fatalf("unexpected request: %+v", got)
	}
}

func TestShowDefaultsEmptyTitle(t *testing.T) {
	svc := NewService()
	var got Request
	svc.deliver = func(req Request) error {
		got = req
		return nil
	}
	if err := svc.Show(Request{Title: "   "}); err != nil {
		t.Fatalf("show: %v", err)
	}
	if got.Title != "Hoxi" {
		t.Fatalf("title = %q, want Hoxi", got.Title)
	}
}

func TestShowTruncatesLongFields(t *testing.T) {
	svc := NewService()
	var got Request
	svc.deliver = func(req Request) error {
		got = req
		return nil
	}
	longTitle := strings.Repeat("题", maxTitleRunes+20)
	longBody := strings.Repeat("文", maxBodyRunes+20)
	if err := svc.Show(Request{Title: longTitle, Body: longBody}); err != nil {
		t.Fatalf("show: %v", err)
	}
	if len([]rune(got.Title)) != maxTitleRunes {
		t.Fatalf("title runes = %d, want %d", len([]rune(got.Title)), maxTitleRunes)
	}
	if len([]rune(got.Body)) != maxBodyRunes {
		t.Fatalf("body runes = %d, want %d", len([]rune(got.Body)), maxBodyRunes)
	}
}

func TestShowUnavailable(t *testing.T) {
	svc := NewService()
	svc.deliver = nil
	if err := svc.Show(Request{Title: "Hoxi"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestClickDispatchesActiveService(t *testing.T) {
	svc := NewService()
	var got string
	svc.SetOnClick(func(sessionID string) { got = sessionID })
	active.Store(svc)
	t.Cleanup(func() { active.Store(nil) })

	dispatchClick(" ses_9 ")
	if got != "ses_9" {
		t.Fatalf("clicked session = %q, want ses_9", got)
	}
}
