package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var userReqRe = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)

type agyLine struct {
	Type            string `json:"type"`
	CreatedAt       string `json:"created_at"`
	Content         string `json:"content"`
	InputTokens     int64  `json:"input_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ToolCalls       []struct {
		Args map[string]any `json:"args"`
	} `json:"tool_calls"`
}

func loadAntigravityEvents(brainDir string, start, end time.Time) []Event {
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return nil
	}

	var events []Event
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		convID := entry.Name()
		logPath := filepath.Join(brainDir, convID, ".system_generated", "logs", "transcript.jsonl")
		info, err := os.Stat(logPath)
		if err != nil || info.ModTime().Before(start) {
			// File wasn't modified in or after our start window; skip fast.
			continue
		}

		evs := parseAntigravityFile(logPath, convID, start, end)
		events = append(events, evs...)
	}
	return events
}

func parseAntigravityFile(path, convID string, start, end time.Time) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var events []Event
	inferredProject := "-"
	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var rec agyLine
			if json.Unmarshal(line, &rec) == nil && rec.CreatedAt != "" {
				ts, parseErr := time.Parse(time.RFC3339Nano, rec.CreatedAt)
				if parseErr == nil {
					ts = ts.Local()
					if inferredProject == "-" {
						for _, tc := range rec.ToolCalls {
							if p := inferProjectFromArgs(tc.Args); p != "" {
								inferredProject = p
								break
							}
						}
					}
					if !ts.Before(start) && ts.Before(end) {
						prompt := ""
						if rec.Type == "USER_INPUT" {
							prompt = extractAGYPrompt(rec.Content)
						}
						events = append(events, Event{
							Tool:         ToolAGY,
							SessionID:    convID,
							Project:      inferredProject,
							Timestamp:    ts,
							Prompt:       prompt,
							InputTokens:  rec.InputTokens,
							CacheTokens:  rec.CacheReadTokens,
							OutputTokens: rec.OutputTokens,
						})
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}

	// Backfill inferred project onto earlier events in the same file.
	if inferredProject != "-" {
		for i := range events {
			if events[i].Project == "-" {
				events[i].Project = inferredProject
			}
		}
	}
	return events
}

func extractAGYPrompt(content string) string {
	if m := userReqRe.FindStringSubmatch(content); len(m) > 1 {
		return cleanSummary(m[1])
	}
	return cleanSummary(content)
}

func inferProjectFromArgs(args map[string]any) string {
	for _, key := range []string{"Path", "path", "Cwd", "cwd"} {
		val, ok := args[key].(string)
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		if p := projectFromPath(val); p != "" {
			return p
		}
	}
	return ""
}

func projectFromPath(p string) string {
	for _, marker := range []string{"/work/individual/", "/work/", "/geniee/cats/", "/geniee/"} {
		if idx := strings.Index(p, marker); idx != -1 {
			rest := p[idx+len(marker):]
			parts := strings.SplitN(rest, "/", 2)
			if len(parts) > 0 && parts[0] != "" {
				return parts[0]
			}
		}
	}
	if strings.Contains(p, "/dotfiles/") {
		return "dotfiles"
	}
	return ""
}

type ccLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
}

type ccMessage struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		InputTokens              int64 `json:"input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
	} `json:"usage"`
}

func loadClaudeCodeEvents(projectsDir string, start, end time.Time) []Event {
	projDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var events []Event
	for _, dir := range projDirs {
		if !dir.IsDir() {
			continue
		}
		slug := dir.Name()
		fullDir := filepath.Join(projectsDir, slug)
		files, err := os.ReadDir(fullDir)
		if err != nil {
			continue
		}
		defaultProj := cleanCCProjectSlug(slug)

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil || info.ModTime().Before(start) {
				continue
			}
			sessionID := strings.TrimSuffix(f.Name(), ".jsonl")
			evs := parseClaudeCodeFile(filepath.Join(fullDir, f.Name()), sessionID, defaultProj, start, end)
			events = append(events, evs...)
		}
	}
	return events
}

func parseClaudeCodeFile(path, sessionID, defaultProj string, start, end time.Time) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var events []Event
	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var rec ccLine
			if json.Unmarshal(line, &rec) == nil && rec.Timestamp != "" && (rec.Type == "user" || rec.Type == "assistant") {
				ts, parseErr := time.Parse(time.RFC3339Nano, rec.Timestamp)
				if parseErr == nil {
					ts = ts.Local()
					if !ts.Before(start) && ts.Before(end) {
						proj := defaultProj
						if rec.Cwd != "" {
							if base := filepath.Base(rec.Cwd); base != "" && base != "/" && base != "." {
								proj = base
							}
						}

						var msg ccMessage
						_ = json.Unmarshal(rec.Message, &msg)

						ev := Event{
							Tool:      ToolCC,
							SessionID: sessionID,
							Project:   proj,
							Timestamp: ts,
						}
						if rec.Type == "user" {
							ev.Prompt = extractCCPrompt(msg.Content)
						}
						if msg.Usage != nil {
							ev.InputTokens = msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens
							ev.CacheTokens = msg.Usage.CacheReadInputTokens
							ev.OutputTokens = msg.Usage.OutputTokens
						}
						events = append(events, ev)
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}
	return events
}

func extractCCPrompt(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if isCCInternalTag(str) {
			return ""
		}
		return cleanSummary(str)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				if isCCInternalTag(p.Text) {
					continue
				}
				return cleanSummary(p.Text)
			}
		}
	}
	return ""
}

func isCCInternalTag(s string) bool {
	return strings.HasPrefix(s, "<command-message>") ||
		strings.HasPrefix(s, "<command-name>") ||
		strings.HasPrefix(s, "<local-command")
}

func cleanCCProjectSlug(slug string) string {
	// e.g. "-Users-daiki-fukushima-work-obsidian-git" -> "obsidian-git"
	if strings.HasPrefix(slug, "-private-tmp-") {
		return "tmp-scratch"
	}
	parts := strings.Split(strings.TrimPrefix(slug, "-"), "-")
	if len(parts) >= 3 && parts[0] == "Users" {
		// Find common container dirs and take what follows.
		for i, p := range parts {
			if (p == "work" || p == "individual" || p == "cats" || p == "geniee") && i+1 < len(parts) {
				if p == "work" && i+1 < len(parts) && parts[i+1] == "individual" {
					continue
				}
				if p == "geniee" && i+1 < len(parts) && parts[i+1] == "cats" {
					continue
				}
				return strings.Join(parts[i+1:], "-")
			}
		}
		if len(parts) > 3 {
			return strings.Join(parts[3:], "-")
		}
		return "home"
	}
	return slug
}

func cleanSummary(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
