package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// work-mcp is a protocol bridge, not another catalog owner: all persistence,
// seller authorization and publication approvals remain inside the desktop.
func runWorkMCPCommand(ctx context.Context, args []string, cwd, defaultAddress string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi work-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sessionID, storeID, directory, address string
	fs.StringVar(&sessionID, "session", "", "managed Hoxi session ID")
	fs.StringVar(&storeID, "store", "", "bound Work store ID")
	fs.StringVar(&directory, "dir", cwd, "exact session directory")
	fs.StringVar(&address, "address", defaultAddress, "local Hoxi gateway address")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || sessionID == "" || storeID == "" || directory == "" {
		fmt.Fprintln(stderr, "work-mcp requires --session, --store and --dir")
		return 2
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if address == "" {
		address = defaultAddress
	}
	candidate := NewClient(address, true)
	u, err := url.Parse(candidate.BaseURL())
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
		(u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		fmt.Fprintln(stderr, "work-mcp address must be a literal loopback HTTP gateway")
		return 2
	}
	client, cleanup, err := ConnectOrSpawn(ctx, address, directory, nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer cleanup()
	if client.token == "" {
		fmt.Fprintln(stderr, "Work MCP requires an authenticated local Hoxi gateway")
		return 1
	}
	endpoint := client.BaseURL() + "/api/hoxi/work/mcp?" + url.Values{
		"sessionID": {sessionID}, "directory": {directory}, "storeId": {storeID},
	}.Encode()
	httpClient := &http.Client{Transport: workMCPAuthTransport{base: client.http.Transport, token: client.token},
		Timeout: 20 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	remote, err := mcp.NewClient(&mcp.Implementation{Name: "hoxi-work-stdio", Version: buildinfo.Version}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}, nil)
	if err != nil {
		fmt.Fprintln(stderr, "Cannot connect Work MCP:", err)
		return 1
	}
	defer remote.Close()
	server, err := proxyWorkMCP(ctx, remote)
	if err == nil {
		reader, ok := stdin.(io.ReadCloser)
		if !ok {
			reader = io.NopCloser(stdin)
		}
		writer, ok := stdout.(io.WriteCloser)
		if !ok {
			writer = workMCPWriter{stdout}
		}
		err = server.Run(ctx, &mcp.IOTransport{Reader: reader, Writer: writer, MaxLineLength: 1 << 20})
	}
	if err != nil {
		fmt.Fprintln(stderr, "Work MCP:", err)
		return 1
	}
	return 0
}

type workMCPAuthTransport struct {
	base  http.RoundTripper
	token string
}

func (t workMCPAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	request := r.Clone(r.Context())
	request.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(request)
}

type workMCPWriter struct{ io.Writer }

func (workMCPWriter) Close() error { return nil }

func proxyWorkMCP(ctx context.Context, remote *mcp.ClientSession) (*mcp.Server, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "hoxi-work", Version: buildinfo.Version}, nil)
	for tool, err := range remote.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return remote.CallTool(ctx, &mcp.CallToolParams{Name: req.Params.Name, Arguments: req.Params.Arguments})
		})
	}
	for template, err := range remote.ResourceTemplates(ctx, nil) {
		if err != nil {
			return nil, err
		}
		server.AddResourceTemplate(template, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return remote.ReadResource(ctx, req.Params)
		})
	}
	for prompt, err := range remote.Prompts(ctx, nil) {
		if err != nil {
			return nil, err
		}
		server.AddPrompt(prompt, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return remote.GetPrompt(ctx, req.Params)
		})
	}
	return server, nil
}
