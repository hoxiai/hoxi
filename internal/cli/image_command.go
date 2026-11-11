package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

type cliStringSlice []string

func (s *cliStringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *cliStringSlice) Set(val string) error {
	for _, part := range strings.Split(val, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			*s = append(*s, trimmed)
		}
	}
	return nil
}

func runImageCommand(
	ctx context.Context,
	args []string,
	cwd string,
	defaultBridgeAddr string,
	bootHeadless HeadlessBootFunc,
	stdout, stderr io.Writer,
) int {
	sub := "generate"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "generate", "gen", "models", "list", "help":
			sub = strings.ToLower(strings.TrimSpace(args[0]))
			rest = args[1:]
		}
	}
	if sub == "help" {
		printImageHelp(stdout)
		return 0
	}

	fs := flag.NewFlagSet("hoxi image", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		modelSpec   string
		providerID  string
		aspectRatio string
		imageSize   string
		count       int
		outPath     string
		dir         string
		bridgeAddr  string
		sessionID   string
		promptFlag  string
		inputs      cliStringSlice
		showHelp    bool
	)
	fs.StringVar(&modelSpec, "model", "", "image model override (provider/model or model-id)")
	fs.StringVar(&modelSpec, "m", "", "shorthand for --model")
	fs.StringVar(&providerID, "provider", "", "provider ID override")
	fs.StringVar(&aspectRatio, "aspect", "1:1", "aspect ratio: 1:1 | 16:9 | 9:16 | 4:3 | 3:4")
	fs.StringVar(&imageSize, "size", "1K", "resolution tier: 1K | 2K | 4K")
	fs.IntVar(&count, "count", 1, "number of images to generate (1-4)")
	fs.StringVar(&outPath, "out", "", "workspace-relative output file path (e.g. assets/hero.png)")
	fs.StringVar(&outPath, "o", "", "shorthand for --out")
	fs.Var(&inputs, "in", "workspace-relative reference image path (repeatable)")
	fs.StringVar(&dir, "dir", cwd, "workspace directory")
	fs.StringVar(&bridgeAddr, "address", defaultBridgeAddr, "Hoxi gateway loopback address")
	fs.StringVar(&sessionID, "session", "", "optional session ID")
	fs.StringVar(&promptFlag, "prompt", "", "image generation prompt")
	fs.StringVar(&promptFlag, "p", "", "shorthand for --prompt")
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")

	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if showHelp {
		printImageHelp(stdout)
		return 0
	}
	if absDir, err := filepath.Abs(dir); err == nil {
		dir = absDir
	}

	client, cleanup, err := ConnectOrSpawn(ctx, bridgeAddr, dir, bootHeadless)
	if err != nil {
		fmt.Fprintf(stderr, "Error connecting to Hoxi gateway: %v\n", err)
		return 1
	}
	defer cleanup()

	if sub == "models" || sub == "list" {
		var caps struct {
			OK          bool `json:"ok"`
			ImageModels []struct {
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
				Name       string `json:"name"`
				Protocol   string `json:"protocol"`
			} `json:"imageModels"`
		}
		if err := client.doJSON(ctx, http.MethodGet, "/api/hoxi/media/capabilities", nil, &caps); err != nil {
			fmt.Fprintf(stderr, "Error querying media capabilities: %v\n", err)
			return 1
		}
		if len(caps.ImageModels) == 0 {
			fmt.Fprintln(stdout, "No image generation models configured.")
			return 0
		}
		for _, m := range caps.ImageModels {
			label := strings.TrimSpace(m.Name)
			if label == "" || label == m.ModelID {
				fmt.Fprintf(stdout, "%s/%s (%s)\n", m.ProviderID, m.ModelID, m.Protocol)
			} else {
				fmt.Fprintf(stdout, "%s/%s — %s (%s)\n", m.ProviderID, m.ModelID, label, m.Protocol)
			}
		}
		return 0
	}

	prompt := strings.TrimSpace(promptFlag)
	if prompt == "" && fs.NArg() > 0 {
		prompt = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if prompt == "" {
		fmt.Fprintln(stderr, "Error: image prompt is required (usage: hoxi image generate \"<prompt>\" [--out path])")
		return 2
	}

	modelID := strings.TrimSpace(modelSpec)
	provID := strings.TrimSpace(providerID)
	if provID == "" && strings.Contains(modelID, "/") {
		parts := strings.SplitN(modelID, "/", 2)
		provID = strings.TrimSpace(parts[0])
		modelID = strings.TrimSpace(parts[1])
	}

	reqBody := map[string]any{
		"prompt":      prompt,
		"providerID":  provID,
		"modelID":     modelID,
		"aspectRatio": aspectRatio,
		"imageSize":   imageSize,
		"count":       count,
		"sessionID":   sessionID,
		"directory":   dir,
		"inputPaths":  []string(inputs),
		"outputPath":  outPath,
	}
	var res struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
		Text       string `json:"text"`
		Images     []struct {
			Filename string `json:"filename"`
			Mime     string `json:"mime"`
			URL      string `json:"url"`
			Path     string `json:"path"`
		} `json:"images"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/api/hoxi/media/image", reqBody, &res); err != nil {
		fmt.Fprintf(stderr, "Error generating image: %v\n", err)
		return 1
	}
	if !res.OK {
		fmt.Fprintf(stderr, "Error generating image: %s\n", firstNonEmpty(res.Error, "unknown error"))
		return 1
	}

	fmt.Fprintf(stdout, "Generated %d image(s) via %s/%s\n", len(res.Images), res.ProviderID, res.ModelID)
	if strings.TrimSpace(res.Text) != "" {
		fmt.Fprintf(stdout, "Notes: %s\n", strings.TrimSpace(res.Text))
	}
	for idx, img := range res.Images {
		loc := firstNonEmpty(img.Path, img.URL, img.Filename)
		fmt.Fprintf(stdout, "  [%d] %s (%s)\n", idx+1, loc, img.Mime)
	}
	return 0
}

func printImageHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  hoxi image generate "<prompt>" [flags]   Generate or edit images via configured image models
  hoxi image models                        List configured image generation models

Flags:
  -o, --out <path>       Workspace-relative output file path (e.g. public/banner.png)
      --in <path>        Reference image inside workspace (repeatable for image editing)
      --aspect <ratio>   Aspect ratio: 1:1 (default) | 16:9 | 9:16 | 4:3 | 3:4
      --size <tier>      Resolution tier: 1K (default) | 2K | 4K
      --count <n>        Number of images to generate (1-4)
  -m, --model <id>       Image model override (provider/model or model-id)
      --dir <path>       Workspace directory (defaults to current working directory)`)
}
