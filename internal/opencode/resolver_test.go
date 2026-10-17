package opencode

import (
	"strings"
	"testing"
)

func TestOpenCodeResolverAndPathEnrichment(t *testing.T) {
	EnrichSystemPath()

	dlURL, isZip, err := OfficialReleaseURL("darwin", "arm64")
	if err != nil || !isZip || !strings.HasSuffix(dlURL, "/opencode-darwin-arm64.zip") {
		t.Fatalf("unexpected darwin-arm64 release URL: url=%q isZip=%v err=%v", dlURL, isZip, err)
	}

	linuxURL, linuxZip, err := OfficialReleaseURL("linux", "amd64")
	if err != nil || linuxZip || !strings.HasSuffix(linuxURL, "/opencode-linux-x64.tar.gz") {
		t.Fatalf("unexpected linux-amd64 release URL: url=%q isZip=%v err=%v", linuxURL, linuxZip, err)
	}
}
