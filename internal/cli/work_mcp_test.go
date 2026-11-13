package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/workcatalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkMCPStdioBridgeUsesStandardProtocolAndLocalAuthentication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	directory := t.TempDir()
	catalog := workcatalog.NewService(workcatalog.NewStore(t.TempDir()))
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/api/hoxi/bootstrap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"gatewayToken":"local-test-token"}`))
	})
	mcpHandler := workcatalog.NewMCPHandler(catalog, func(*http.Request) (workcatalog.Connection, error) {
		return workcatalog.Connection{Scope: workcatalog.Scope{StoreID: "store-a", SellerLoginID: "factory-a"},
			Platform: func() (workcatalog.Platform, error) {
				return workcatalog.NewAlibaba1688FromEnv("UNCONFIGURED_WORK_MCP_TEST", "")
			}}, nil
	})
	mux.HandleFunc("/api/hoxi/work/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-test-token" || r.URL.Query().Get("sessionID") != "ses_work" ||
			r.URL.Query().Get("storeId") != "store-a" || r.URL.Query().Get("directory") != directory {
			t.Error("stdio bridge lost authentication or scope")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	inputR, inputW := io.Pipe()
	outputR, outputW := io.Pipe()
	defer inputR.Close()
	defer inputW.Close()
	defer outputR.Close()
	defer outputW.Close()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runWorkMCPCommand(ctx, []string{"--session", "ses_work", "--store", "store-a", "--dir", directory, "--address", srv.URL}, directory, srv.URL, inputR, outputW, &stderr)
	}()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-test"}, nil).Connect(ctx, &mcp.IOTransport{Reader: outputR, Writer: inputW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 10 {
		t.Fatal(tools, err)
	}
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "work_store_status", Arguments: map[string]any{}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatal(result, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 || stderr.Len() != 0 {
			t.Fatal("stdio bridge failed", code, stderr.String())
		}
	case <-ctx.Done():
		t.Fatal("stdio bridge did not exit after client closed")
	}
}

func TestWorkMCPRejectsNonLoopbackAddressesBeforeConnecting(t *testing.T) {
	for _, address := range []string{"https://example.com", "http://127.0.0.1.evil.example:5410", "http://user:pass@127.0.0.1:5410", "http://127.0.0.1:5410/path"} {
		var stdout, stderr bytes.Buffer
		code := runWorkMCPCommand(context.Background(), []string{"--session", "ses_work", "--store", "store-a", "--address", address}, t.TempDir(), address, strings.NewReader(""), &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 {
			t.Fatal("invalid MCP bridge address accepted", address, code, stdout.String())
		}
	}
}
