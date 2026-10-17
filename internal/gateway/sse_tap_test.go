package gateway

import (
	"bytes"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSSETapBoundedCaptureAndRecovery(t *testing.T) {
	for _, chunkSize := range []int{1, 257, maxSSETapLineSize + 128} {
		t.Run(strconv.Itoa(chunkSize), func(t *testing.T) {
			boundary := strings.Repeat("b", maxSSETapLineSize)
			oversized := strings.Repeat("x", maxSSETapLineSize+1)
			stream := "data: first\r\n\n" + boundary + "\n" + oversized + "\r\ndata: recovered\nunfinished"
			var lines []string
			tap := newSSETapReadCloser(io.NopCloser(strings.NewReader(stream)), func(line string) {
				lines = append(lines, line)
			}).(*sseTapReadCloser)
			defer tap.Close()
			var raw bytes.Buffer
			buf := make([]byte, chunkSize)
			for {
				n, err := tap.Read(buf)
				raw.Write(buf[:n])
				if len(tap.buf) > maxSSETapLineSize {
					t.Fatal("capture exceeded line limit")
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if raw.String() != stream {
				t.Fatal("tap changed raw stream bytes")
			}
			if want := []string{"data: first", "", boundary, "data: recovered"}; !reflect.DeepEqual(lines, want) {
				t.Fatalf("unexpected captured line lengths: got %d lines, want %d", len(lines), len(want))
			}
		})
	}
}
