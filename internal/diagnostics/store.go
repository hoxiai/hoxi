// Package diagnostics stores bounded, metadata-only local diagnostics.
package diagnostics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxBytes     = int64(100 * 1024 * 1024)
	segmentBytes = int64(5 * 1024 * 1024)
	retention    = 7 * 24 * time.Hour
	maxLineBytes = 4096
)

type Entry struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`
	Level      string    `json:"level"`
	Source     string    `json:"source"`
	Message    string    `json:"message"`
	SessionID  string    `json:"sessionID,omitempty"`
	ProviderID string    `json:"providerID,omitempty"`
	ModelID    string    `json:"modelID,omitempty"`
	Status     int       `json:"status,omitempty"`
	DurationMs int64     `json:"durationMs,omitempty"`
	ID         string    `json:"id"`
	ExitCode   *int64    `json:"exitCode,omitempty"`
}

type store struct {
	mu              sync.Mutex
	directory       string
	err             error
	budget, segment int64
	now             func() time.Time
	openDirectory   func(string) error
}

var global struct {
	sync.Mutex
	s *store
}

// Initialize is idempotent; failures are exposed by the HTTP handler. Record
// also initializes lazily so headless execution needs no UI or startup hook.
func Initialize() error {
	global.Lock()
	defer global.Unlock()
	if global.s == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		global.s = &store{directory: filepath.Join(home, ".hoxi", "logs"), budget: maxBytes, segment: segmentBytes, now: time.Now}
	}
	s := global.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = s.prepare()
	if s.err == nil {
		s.err = s.prune(0)
	}
	return s.err
}

func defaultStore() *store {
	global.Lock()
	s := global.s
	global.Unlock()
	if s == nil {
		_ = Initialize()
		global.Lock()
		s = global.s
		global.Unlock()
	}
	return s
}

// Record accepts classification summaries, never prompts, output or raw errors.
// It is best effort and never propagates storage failures to execution paths.
func Record(e Entry) {
	if s := defaultStore(); s != nil {
		s.record(e)
	}
}

var secret = regexp.MustCompile(`(?i)(bearer\s+\S+|(?:sk-|ghp_|github_pat_|sk-ant-)[a-z0-9_\-]+|(?:api[_-]?key|token|password|secret|authorization)\s*[=:]\s*[^\s,;]+)`)
var urlPattern = regexp.MustCompile(`https?://[^\s]+`)
var privatePath = regexp.MustCompile(`(?:/Users/|/home/|[A-Za-z]:\\Users\\)[^\s]+`)
var payload = regexp.MustCompile(`(?i)\b(prompt|output|headers|messages|completion|response body)\b\s*[=:]`)

func clean(v string, limit int) string {
	// Bound regexp work as well as the eventual encoded record.
	if len(v) > 8192 {
		v = v[:8192]
	}
	if payload.MatchString(v) {
		return "[content omitted]"
	}
	v = secret.ReplaceAllString(v, "[redacted]")
	v = urlPattern.ReplaceAllString(v, "[url]")
	v = privatePath.ReplaceAllString(v, "[path]")
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, v)
	if len(v) > limit {
		v = v[:limit]
		for !utf8.ValidString(v) {
			v = v[:len(v)-1]
		}
	}
	return v
}

func sanitize(e Entry) Entry {
	if e.Kind != "request" {
		e.Kind = "system"
	}
	e.Level = strings.ToUpper(e.Level)
	switch e.Level {
	case "DEBUG", "INFO", "WARN", "ERROR":
	default:
		e.Level = "INFO"
	}
	e.Source = clean(e.Source, 80)
	e.Message = clean(e.Message, 256)
	e.SessionID = clean(e.SessionID, 128)
	e.ProviderID = clean(e.ProviderID, 128)
	e.ModelID = clean(e.ModelID, 128)
	e.ID = clean(e.ID, 80)
	return e
}

func (s *store) prepare() error {
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(s.directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe diagnostics directory")
	}
	return os.Chmod(s.directory, 0700)
}

type logFile struct {
	path     string
	size     int64
	modified time.Time
}

func (s *store) files() ([]logFile, error) {
	items, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, err
	}
	var files []logFile
	for _, item := range items {
		name := item.Name()
		if !(strings.HasPrefix(name, "system-") || strings.HasPrefix(name, "request-")) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := item.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("unsafe diagnostics file")
		}
		if err := os.Chmod(filepath.Join(s.directory, name), 0600); err != nil {
			return nil, err
		}
		files = append(files, logFile{filepath.Join(s.directory, name), info.Size(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.Before(files[j].modified) })
	return files, nil
}

func (s *store) prune(incoming int64) error {
	files, err := s.files()
	if err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.size
	}
	for _, f := range files {
		if f.modified.Before(s.now().Add(-retention)) || total+incoming > s.budget {
			if err := os.Remove(f.path); err != nil {
				return err
			}
			total -= f.size
		}
	}
	return nil
}

func (s *store) record(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = s.append(e)
}

func (s *store) append(e Entry) error {
	if err := s.prepare(); err != nil {
		return err
	}
	now := s.now().UTC()
	e.Time = now
	e = sanitize(e)
	if e.ID == "" {
		e.ID = fmt.Sprintf("%d", now.UnixNano())
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxLineBytes {
		return errors.New("diagnostics entry too large")
	}
	if err := s.prune(int64(len(data))); err != nil {
		return err
	}
	files, err := s.files()
	if err != nil {
		return err
	}
	name := ""
	prefix := e.Kind + "-" + now.Format("20060102") + "-"
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		if strings.HasPrefix(filepath.Base(f.path), prefix) && f.size+int64(len(data)) <= s.segment {
			name = f.path
			break
		}
	}
	flags := os.O_WRONLY | os.O_APPEND
	if name == "" {
		name = filepath.Join(s.directory, prefix+fmt.Sprint(now.UnixNano())+".jsonl")
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(name, flags, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

type filter struct {
	kind, level, session, provider, query string
	limit                                 int
}

func (s *store) entries(f filter) ([]Entry, error) {
	f.level = strings.ToUpper(f.level)
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Entry, 0)
	if err := s.prepare(); err != nil {
		return result, err
	}
	if err := s.prune(0); err != nil {
		return result, err
	}
	files, err := s.files()
	if err != nil {
		return result, err
	}
	for _, file := range files {
		input, err := os.Open(file.path)
		if err != nil {
			return result, err
		}
		scan := bufio.NewScanner(input)
		scan.Buffer(make([]byte, maxLineBytes), maxLineBytes)
		for scan.Scan() {
			var e Entry
			if json.Unmarshal(scan.Bytes(), &e) != nil || e.Time.Before(s.now().Add(-retention)) {
				continue
			}
			e = sanitize(e)
			if f.kind != "" && e.Kind != f.kind || f.level != "" && e.Level != f.level || f.session != "" && e.SessionID != f.session || f.provider != "" && e.ProviderID != f.provider {
				continue
			}
			if f.query != "" {
				b, _ := json.Marshal(e)
				if !strings.Contains(strings.ToLower(string(b)), strings.ToLower(f.query)) {
					continue
				}
			}
			result = append(result, e)
			// Keep memory bounded even when querying all retained files.
			if len(result) > f.limit*2 {
				sort.SliceStable(result, func(i, j int) bool { return result[i].Time.After(result[j].Time) })
				result = result[:f.limit]
			}
		}
		err = scan.Err()
		input.Close()
		if err != nil {
			return result, err
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Time.After(result[j].Time) })
	if len(result) > f.limit {
		result = result[:f.limit]
	}
	return result, s.err
}
