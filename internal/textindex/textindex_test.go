package textindex

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTokenizeSplitsIdentifiersAndStopwords(t *testing.T) {
	got := Tokenize("HandleRequest parseHTTP2 with the user_id")
	want := []string{"handle", "request", "parse", "http2", "user", "id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize() = %#v, want %#v", got, want)
	}
}

func TestScoreRanksDistinctiveTerms(t *testing.T) {
	df := map[string]int{"auth": 1, "token": 2}
	hot := Counts("auth token refresh")
	cold := Counts("token cache")
	if Score([]string{"auth"}, hot, df, 2, 3, 4) <= Score([]string{"auth"}, cold, df, 2, 2, 4) {
		t.Fatal("rarer term should score higher")
	}
}

func TestSnippetAndClip(t *testing.T) {
	snippet := Snippet("a\n\nb\nc\nd\ne\nf\ng", 3, 100)
	if strings.Count(snippet, "\n") != 2 || strings.Contains(snippet, "\n\n") {
		t.Fatalf("unexpected snippet: %q", snippet)
	}
	if Clip("abcdef", 3) != "abc…" {
		t.Fatalf("Clip() = %q", Clip("abcdef", 3))
	}
	if Clip("abc", 3) != "abc" {
		t.Fatal("short values must not be clipped")
	}
}

func TestTokenizeKeepsStatusCodes(t *testing.T) {
	got := strings.Join(Tokenize("upstream returned 502 on port 8080 (retry 3, v2)"), " ")
	for _, want := range []string{"502", "8080", "v2"} {
		if !strings.Contains(" "+got+" ", " "+want+" ") {
			t.Fatalf("Tokenize dropped %q: %q", want, got)
		}
	}
	if strings.Contains(" "+got+" ", " 3 ") {
		t.Fatalf("single digits should still be dropped: %q", got)
	}
}

func TestSnippetTruncationKeepsUTF8(t *testing.T) {
	for n := 1; n < 13; n++ {
		if v := Snippet("你好世界你好", 5, n); !utf8.ValidString(v) {
			t.Fatalf("Snippet(maxChars=%d) produced invalid UTF-8: %q", n, v)
		}
	}
}
