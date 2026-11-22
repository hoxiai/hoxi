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

func reorderVideoFlagsFirst(args []string) []string {
	valueFlags := map[string]bool{
		"-m": true, "--model": true,
		"--provider":   true,
		"--aspect":     true,
		"--resolution": true,
		"--duration":   true,
		"-o":           true, "--out": true,
		"--in":      true,
		"--dir":     true,
		"--address": true,
		"--session": true,
		"-p":        true, "--prompt": true,
	}
	var flags []string
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && valueFlags[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, a)
		}
	}
	return append(flags, pos...)
}

func runVideoCommand(
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
		printVideoHelp(stdout)
		return 0
	}

	fs := flag.NewFlagSet("hoxi video", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		modelSpec   string
		providerID  string
		aspectRatio string
		resolution  string
		durationSec int
		outPath     string
		dir         string
		bridgeAddr  string
		sessionID   string
		promptFlag  string
		inputs      cliStringSlice
		showHelp    bool
	)
	fs.StringVar(&modelSpec, "model", "", "video model override (provider/model or model-id)")
	fs.StringVar(&modelSpec, "m", "", "shorthand for --model")
	fs.StringVar(&providerID, "provider", "", "provider ID override")
	fs.StringVar(&aspectRatio, "aspect", "16:9", "aspect ratio: 16:9 | 9:16 | 1:1 | 4:3 | 3:4")
	fs.StringVar(&resolution, "resolution", "720p", "resolution tier: 720p | 1080p")
	fs.IntVar(&durationSec, "duration", 0, "target clip duration in seconds (2-20)")
	fs.StringVar(&outPath, "out", "", "workspace-relative output file path (e.g. public/intro.mp4)")
	fs.StringVar(&outPath, "o", "", "shorthand for --out")
	fs.Var(&inputs, "in", "workspace-relative first-frame reference image path")
	fs.StringVar(&dir, "dir", cwd, "workspace directory")
	fs.StringVar(&bridgeAddr, "address", defaultBridgeAddr, "Hoxi gateway loopback address")
	fs.StringVar(&sessionID, "session", "", "optional session ID")
	fs.StringVar(&promptFlag, "prompt", "", "video generation prompt")
	fs.StringVar(&promptFlag, "p", "", "shorthand for --prompt")
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")

	if err := fs.Parse(reorderVideoFlagsFirst(rest)); err != nil {
		return 2
	}
	if showHelp {
		printVideoHelp(stdout)
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
			VideoModels []struct {
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
				Name       string `json:"name"`
			} `json:"videoModels"`
		}
		if err := client.doJSON(ctx, http.MethodGet, "/api/hoxi/media/capabilities", nil, &caps); err != nil {
			fmt.Fprintf(stderr, "Error querying media capabilities: %v\n", err)
			return 1
		}
		if len(caps.VideoModels) == 0 {
			fmt.Fprintln(stdout, "No video generation models configured.")
			return 0
		}
		for _, m := range caps.VideoModels {
			label := strings.TrimSpace(m.Name)
			if label == "" || label == m.ModelID {
				fmt.Fprintf(stdout, "%s/%s\n", m.ProviderID, m.ModelID)
			} else {
				fmt.Fprintf(stdout, "%s/%s — %s\n", m.ProviderID, m.ModelID, label)
			}
		}
		return 0
	}

	prompt := strings.TrimSpace(promptFlag)
	if prompt == "" && fs.NArg() > 0 {
		prompt = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if prompt == "" {
		fmt.Fprintln(stderr, "Error: video prompt is required (usage: hoxi video generate \"<prompt>\" [--out path])")
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
		"prompt":          prompt,
		"providerID":      provID,
		"modelID":         modelID,
		"aspectRatio":     aspectRatio,
		"resolution":      resolution,
		"durationSeconds": durationSec,
		"sessionID":       sessionID,
		"directory":       dir,
		"inputPaths":      []string(inputs),
		"outputPath":      outPath,
	}
	var res struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
		Text       string `json:"text"`
		Videos     []struct {
			Filename string `json:"filename"`
			Mime     string `json:"mime"`
			URL      string `json:"url"`
			Path     string `json:"path"`
		} `json:"videos"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/api/hoxi/media/video", reqBody, &res); err != nil {
		fmt.Fprintf(stderr, "Error generating video: %v\n", err)
		return 1
	}
	if !res.OK {
		fmt.Fprintf(stderr, "Error generating video: %s\n", firstNonEmpty(res.Error, "unknown error"))
		return 1
	}

	fmt.Fprintf(stdout, "Generated %d video(s) via %s/%s\n", len(res.Videos), res.ProviderID, res.ModelID)
	if strings.TrimSpace(res.Text) != "" {
		fmt.Fprintf(stdout, "Notes: %s\n", strings.TrimSpace(res.Text))
	}
	for idx, vid := range res.Videos {
		loc := firstNonEmpty(vid.Path, vid.URL, vid.Filename)
		fmt.Fprintf(stdout, "  [%d] %s (%s)\n", idx+1, loc, vid.Mime)
	}
	return 0
}

func printVideoHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  hoxi video generate "<prompt>" [flags]   Generate videos via native/configured video models
  hoxi video models                        List configured video generation models

Flags:
  -o, --out <path>          Workspace-relative output file path (e.g. public/intro.mp4)
      --in <path>           First-frame reference image inside workspace
      --aspect <ratio>      Aspect ratio: 16:9 (default) | 9:16 | 1:1 | 4:3 | 3:4
      --resolution <tier>   Resolution tier: 720p (default) | 1080p
      --duration <seconds>  Target duration in seconds (2-20)
  -m, --model <id>          Video model override (provider/model or model-id)
      --dir <path>          Workspace directory (defaults to current working directory)`)
}
