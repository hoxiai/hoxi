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

func reorderSpeechFlagsFirst(args []string) []string {
	valueFlags := map[string]bool{
		"-m": true, "--model": true,
		"--provider": true,
		"--voice":    true,
		"--format":   true,
		"--speed":    true,
		"-o":         true, "--out": true,
		"--dir":     true,
		"--address": true,
		"--session": true,
		"-p":        true, "--prompt": true,
		"-t": true, "--text": true,
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

func runSpeechCommand(
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
		printSpeechHelp(stdout)
		return 0
	}

	fs := flag.NewFlagSet("hoxi speech", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		modelSpec  string
		providerID string
		voice      string
		format     string
		speed      float64
		outPath    string
		dir        string
		bridgeAddr string
		sessionID  string
		textFlag   string
		showHelp   bool
	)
	fs.StringVar(&modelSpec, "model", "", "speech model override (provider/model or model-id)")
	fs.StringVar(&modelSpec, "m", "", "shorthand for --model")
	fs.StringVar(&providerID, "provider", "", "provider ID override")
	fs.StringVar(&voice, "voice", "", "voice name (e.g. alloy, nova, Kore)")
	fs.StringVar(&format, "format", "mp3", "audio format: mp3 | wav | opus | flac | aac")
	fs.Float64Var(&speed, "speed", 1.0, "speech speed (0.25-4.0)")
	fs.StringVar(&outPath, "out", "", "workspace-relative output file path (e.g. public/voiceover.mp3)")
	fs.StringVar(&outPath, "o", "", "shorthand for --out")
	fs.StringVar(&dir, "dir", cwd, "workspace directory")
	fs.StringVar(&bridgeAddr, "address", defaultBridgeAddr, "Hoxi gateway loopback address")
	fs.StringVar(&sessionID, "session", "", "optional session ID")
	fs.StringVar(&textFlag, "text", "", "text to synthesize")
	fs.StringVar(&textFlag, "t", "", "shorthand for --text")
	fs.StringVar(&textFlag, "prompt", "", "alias for --text")
	fs.StringVar(&textFlag, "p", "", "shorthand for --prompt")
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")

	if err := fs.Parse(reorderSpeechFlagsFirst(rest)); err != nil {
		return 2
	}
	if showHelp {
		printSpeechHelp(stdout)
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
			OK           bool `json:"ok"`
			SpeechModels []struct {
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
				Name       string `json:"name"`
			} `json:"speechModels"`
		}
		if err := client.doJSON(ctx, http.MethodGet, "/api/hoxi/media/capabilities", nil, &caps); err != nil {
			fmt.Fprintf(stderr, "Error querying media capabilities: %v\n", err)
			return 1
		}
		if len(caps.SpeechModels) == 0 {
			fmt.Fprintln(stdout, "No speech synthesis models configured.")
			return 0
		}
		for _, m := range caps.SpeechModels {
			label := strings.TrimSpace(m.Name)
			if label == "" || label == m.ModelID {
				fmt.Fprintf(stdout, "%s/%s\n", m.ProviderID, m.ModelID)
			} else {
				fmt.Fprintf(stdout, "%s/%s — %s\n", m.ProviderID, m.ModelID, label)
			}
		}
		return 0
	}

	text := strings.TrimSpace(textFlag)
	if text == "" && fs.NArg() > 0 {
		text = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if text == "" {
		fmt.Fprintln(stderr, "Error: speech text is required (usage: hoxi speech generate \"<text>\" [--out path])")
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
		"text":       text,
		"providerID": provID,
		"modelID":    modelID,
		"voice":      voice,
		"format":     format,
		"speed":      speed,
		"sessionID":  sessionID,
		"directory":  dir,
		"outputPath": outPath,
	}
	var res struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
		Audios     []struct {
			Filename string `json:"filename"`
			Mime     string `json:"mime"`
			URL      string `json:"url"`
			Path     string `json:"path"`
		} `json:"audios"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/api/hoxi/media/speech", reqBody, &res); err != nil {
		fmt.Fprintf(stderr, "Error synthesizing speech: %v\n", err)
		return 1
	}
	if !res.OK {
		fmt.Fprintf(stderr, "Error synthesizing speech: %s\n", firstNonEmpty(res.Error, "unknown error"))
		return 1
	}

	fmt.Fprintf(stdout, "Synthesized speech via %s/%s\n", res.ProviderID, res.ModelID)
	for idx, aud := range res.Audios {
		loc := firstNonEmpty(aud.Path, aud.URL, aud.Filename)
		fmt.Fprintf(stdout, "  [%d] %s (%s)\n", idx+1, loc, aud.Mime)
	}
	return 0
}

func printSpeechHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  hoxi speech generate "<text>" [flags]   Synthesize speech via native/configured TTS models
  hoxi speech models                      List configured speech synthesis models

Flags:
  -o, --out <path>      Workspace-relative output file path (e.g. public/voiceover.mp3)
      --voice <name>    Voice name (e.g. alloy, nova, Kore)
      --format <fmt>    Output format: mp3 (default) | wav | opus | flac | aac
      --speed <float>   Playback speed (0.25-4.0)
  -m, --model <id>      Speech model override (provider/model or model-id)
      --dir <path>      Workspace directory (defaults to current working directory)`)
}
