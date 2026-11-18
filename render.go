package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiCyan    = "\033[36m"
	ansiMagenta = "\033[35m"
	ansiYellow  = "\033[33m"
)

func renderReport(blocks []ActivityBlock, startDate, endDate time.Time, useColor bool) {
	if len(blocks) == 0 {
		fmt.Printf("No AI activity found for %s .. %s\n",
			startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
		return
	}

	byDay := make(map[string][]ActivityBlock)
	for _, b := range blocks {
		day := b.Start.Format("2006-01-02")
		byDay[day] = append(byDay[day], b)
	}

	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dayKey := d.Format("2006-01-02")
		dayBlocks := byDay[dayKey]
		if len(dayBlocks) == 0 {
			continue
		}
		renderDay(d, dayBlocks, useColor)
		fmt.Println()
	}
}

func renderDay(day time.Time, blocks []ActivityBlock, useColor bool) {
	var totalMin int
	var agyMin, ccMin int
	var totalIn, totalCache, totalOut int64
	var maxTokens int64

	for _, b := range blocks {
		totalMin += b.DurationMin
		if b.Tool == ToolAGY {
			agyMin += b.DurationMin
		} else {
			ccMin += b.DurationMin
		}
		totalIn += b.InputTokens
		totalCache += b.CacheTokens
		totalOut += b.OutputTokens
		if b.TotalTokens > maxTokens {
			maxTokens = b.TotalTokens
		}
	}

	totalTok := totalIn + totalCache + totalOut
	header := fmt.Sprintf("=== %s (%s) | Active: %s (AGY: %s, CC: %s) | Tokens: %s (In: %s, Cache: %s, Out: %s) ===",
		day.Format("2006-01-02"),
		day.Format("Mon"),
		formatDuration(totalMin),
		formatDuration(agyMin),
		formatDuration(ccMin),
		formatTokens(totalTok),
		formatTokens(totalIn),
		formatTokens(totalCache),
		formatTokens(totalOut),
	)
	fmt.Println(colorize(header, ansiBold, useColor))

	// Section 1: 24h Lane (1-hour ticks, 3 chars per hour = 20m resolution)
	fmt.Println()
	fmt.Println(colorize("[24h 稼働マップ]", ansiBold, useColor) + " " +
		colorize("■ Antigravity", ansiCyan, useColor) + "  " +
		colorize("▓ Claude Code", ansiMagenta, useColor))

	// Hour ticks: "0  1  2  ... 23 24"
	var tickLine strings.Builder
	tickLine.WriteString("Hour        ")
	for h := 0; h < 24; h++ {
		tickLine.WriteString(fmt.Sprintf("%-3d", h))
	}
	tickLine.WriteString("24")
	fmt.Println(colorize(tickLine.String(), ansiDim, useColor))

	renderLane("Antigravity", blocks, ToolAGY, "█", ansiCyan, useColor)
	renderLane("Claude Code", blocks, ToolCC, "▓", ansiMagenta, useColor)

	// Section 2: Gantt Chart
	fmt.Println()
	fmt.Println(colorize("[作業タイムライン]", ansiBold, useColor))

	for _, b := range blocks {
		timeRange := fmt.Sprintf("%s-%s (%3dm)",
			b.Start.Format("15:04"), b.End.Format("15:04"), b.DurationMin)

		ganttBar := buildGanttBar(b.Start, b.End, b.Tool, useColor)

		toolBadge := string(b.Tool)
		if b.Tool == ToolAGY {
			toolBadge = colorize("AGY", ansiCyan, useColor)
		} else {
			toolBadge = colorize("CC ", ansiMagenta, useColor)
		}

		proj := padDisplayWidth(truncateDisplayWidth(b.Project, 14), 14)
		tokStr := fmt.Sprintf("%6s", formatTokens(b.TotalTokens))
		tokBar := colorize(buildTokenBar(b.TotalTokens, maxTokens, 5), ansiYellow, useColor)
		summary := truncateDisplayWidth(b.Summary, 42)

		fmt.Printf("%s | %s | %s | %s | %s %s | %s\n",
			timeRange, ganttBar, toolBadge, proj, tokStr, tokBar, summary)
	}
}

func renderLane(label string, blocks []ActivityBlock, tool ToolType, ch, color string, useColor bool) {
	// 24 hours * 3 slots per hour (each slot = 20 minutes) = 72 slots.
	const slots = 72
	active := make([]bool, slots)

	for _, b := range blocks {
		if b.Tool != tool {
			continue
		}
		startMin := b.Start.Hour()*60 + b.Start.Minute()
		endMin := b.End.Hour()*60 + b.End.Minute()
		if endMin <= startMin {
			endMin = startMin + 1
		}
		startSlot := startMin / 20
		endSlot := (endMin - 1) / 20
		if startSlot < 0 {
			startSlot = 0
		}
		if endSlot >= slots {
			endSlot = slots - 1
		}
		for s := startSlot; s <= endSlot; s++ {
			active[s] = true
		}
	}

	var bar strings.Builder
	for s := 0; s < slots; s++ {
		if active[s] {
			bar.WriteString(colorize(ch, color, useColor))
		} else if s%3 == 0 {
			bar.WriteString(colorize("·", ansiDim, useColor))
		} else {
			bar.WriteString(" ")
		}
	}
	fmt.Printf("%-11s |%s|\n", label, bar.String())
}

func buildGanttBar(start, end time.Time, tool ToolType, useColor bool) string {
	// 24 chars wide (1 char per hour 0..23)
	const cols = 24
	startH := start.Hour()
	endH := end.Hour()
	if endH < startH {
		endH = startH
	}

	ch := "█"
	col := ansiCyan
	if tool == ToolCC {
		ch = "▓"
		col = ansiMagenta
	}

	var sb strings.Builder
	for h := 0; h < cols; h++ {
		if h >= startH && h <= endH {
			sb.WriteString(colorize(ch, col, useColor))
		} else {
			sb.WriteString(colorize("·", ansiDim, useColor))
		}
	}
	return sb.String()
}

func buildTokenBar(val, max int64, width int) string {
	if max <= 0 || val <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int((val*int64(width) + max - 1) / max)
	if filled < 1 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func formatDuration(mins int) string {
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	h := mins / 60
	m := mins % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func colorize(s, code string, useColor bool) string {
	if !useColor {
		return s
	}
	return code + s + ansiReset
}

func runeDisplayWidth(r rune) int {
	if r < 128 {
		return 1
	}
	if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		(r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF) {
		return 2
	}
	return 1
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeDisplayWidth(r)
	}
	return w
}

func truncateDisplayWidth(s string, maxW int) string {
	if displayWidth(s) <= maxW {
		return s
	}
	var sb strings.Builder
	w := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if w+rw+1 > maxW {
			sb.WriteRune('…')
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	return sb.String()
}

func padDisplayWidth(s string, targetW int) string {
	w := displayWidth(s)
	if w >= targetW {
		return s
	}
	return s + strings.Repeat(" ", targetW-w)
}
