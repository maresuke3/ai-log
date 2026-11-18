package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ToolType string

const (
	ToolAGY ToolType = "AGY"
	ToolCC  ToolType = "CC"
)

// Event represents a single timestamped interaction turn in a log file.
type Event struct {
	Tool        ToolType
	SessionID   string
	Project     string
	Timestamp   time.Time
	Prompt      string
	InputTokens int64
	CacheTokens int64
	OutputTokens int64
}

// ActivityBlock represents a contiguous burst of AI usage (idle gap <= 30m).
type ActivityBlock struct {
	Tool         ToolType `json:"tool"`
	SessionID    string   `json:"session_id"`
	Project      string   `json:"project"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	DurationMin  int      `json:"duration_min"`
	Summary      string   `json:"summary"`
	InputTokens  int64    `json:"input_tokens"`
	CacheTokens  int64    `json:"cache_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	TotalTokens  int64    `json:"total_tokens"`
}

func main() {
	dateFlag := flag.String("date", "", "Target date (YYYY-MM-DD, default: today)")
	daysFlag := flag.Int("days", 1, "Number of recent days to show (e.g. --days 7)")
	toolFlag := flag.String("tool", "all", "Filter by tool: all, agy, cc")
	jsonFlag := flag.Bool("json", false, "Output JSON")
	noColorFlag := flag.Bool("no-color", false, "Disable ANSI colors")
	flag.Parse()

	now := time.Now()
	endDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if *dateFlag != "" {
		parsed, err := time.ParseInLocation("2006-01-02", *dateFlag, now.Location())
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --date format (expected YYYY-MM-DD): %v\n", err)
			os.Exit(1)
		}
		endDate = parsed
	}

	days := *daysFlag
	if days < 1 {
		days = 1
	}
	startDate := endDate.AddDate(0, 0, -(days - 1))
	rangeEnd := endDate.AddDate(0, 0, 1)

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get home dir: %v\n", err)
		os.Exit(1)
	}

	toolFilter := strings.ToLower(*toolFlag)
	var events []Event

	if toolFilter == "all" || toolFilter == "agy" {
		agyDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
		events = append(events, loadAntigravityEvents(agyDir, startDate, rangeEnd)...)
	}
	if toolFilter == "all" || toolFilter == "cc" {
		ccDir := filepath.Join(home, ".claude", "projects")
		events = append(events, loadClaudeCodeEvents(ccDir, startDate, rangeEnd)...)
	}

	blocks := buildActivityBlocks(events, 30*time.Minute)

	if *jsonFlag {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(blocks)
		return
	}

	useColor := !*noColorFlag && isTerminal(os.Stdout)
	renderReport(blocks, startDate, endDate, useColor)
}

func buildActivityBlocks(events []Event, maxGap time.Duration) []ActivityBlock {
	if len(events) == 0 {
		return nil
	}

	// Group by Tool + SessionID + Day so a resumed session on a different day stays per-day.
	type groupKey struct {
		tool      ToolType
		sessionID string
		day       string
	}
	groups := make(map[groupKey][]Event)
	for _, ev := range events {
		k := groupKey{
			tool:      ev.Tool,
			sessionID: ev.SessionID,
			day:       ev.Timestamp.Format("2006-01-02"),
		}
		groups[k] = append(groups[k], ev)
	}

	var blocks []ActivityBlock
	for _, evs := range groups {
		sort.Slice(evs, func(i, j int) bool {
			return evs[i].Timestamp.Before(evs[j].Timestamp)
		})

		cur := newBlock(evs[0])
		for _, ev := range evs[1:] {
			if ev.Timestamp.Sub(cur.End) > maxGap {
				finalizeBlock(&cur)
				blocks = append(blocks, cur)
				cur = newBlock(ev)
				continue
			}
			cur.End = ev.Timestamp
			cur.InputTokens += ev.InputTokens
			cur.CacheTokens += ev.CacheTokens
			cur.OutputTokens += ev.OutputTokens
			if cur.Summary == "" && ev.Prompt != "" {
				cur.Summary = ev.Prompt
			}
			if cur.Project == "-" && ev.Project != "" && ev.Project != "-" {
				cur.Project = ev.Project
			}
		}
		finalizeBlock(&cur)
		blocks = append(blocks, cur)
	}

	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Start.Equal(blocks[j].Start) {
			return blocks[i].Tool < blocks[j].Tool
		}
		return blocks[i].Start.Before(blocks[j].Start)
	})
	return blocks
}

func newBlock(ev Event) ActivityBlock {
	proj := ev.Project
	if proj == "" {
		proj = "-"
	}
	return ActivityBlock{
		Tool:         ev.Tool,
		SessionID:    ev.SessionID,
		Project:      proj,
		Start:        ev.Timestamp,
		End:          ev.Timestamp,
		Summary:      ev.Prompt,
		InputTokens:  ev.InputTokens,
		CacheTokens:  ev.CacheTokens,
		OutputTokens: ev.OutputTokens,
	}
}

func finalizeBlock(b *ActivityBlock) {
	durMin := int(b.End.Sub(b.Start).Minutes())
	if durMin < 1 {
		durMin = 1
	}
	b.DurationMin = durMin
	b.TotalTokens = b.InputTokens + b.CacheTokens + b.OutputTokens
	if b.Summary == "" {
		b.Summary = "(no prompt)"
	}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
