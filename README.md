# ai-log

Antigravity CLI (`~/.gemini/antigravity-cli/brain`) と Claude Code (`~/.claude/projects`) のローカルログを横断集計し、24時間レーン（1時間目盛り）＋ガントチャートでターミナルに可視化する CLI ツールです。

## Build

```bash
go build -o ~/dotfiles/cmd/ai-log .
```

## Usage

```bash
# 今日の利用状況を表示
ai-log

# 直近7日間を表示
ai-log --days 7

# 特定の日付を指定
ai-log --date 2026-11-18

# ツールで絞り込み (all | agy | cc)
ai-log --tool agy

# JSON 出力
ai-log --json
```
