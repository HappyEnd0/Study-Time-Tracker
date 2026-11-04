# 学习时长统计 · Study Time Tracker

A local-first Windows desktop app for tracking study time from foreground windows, with optional camera face detection and AI-assisted screen analysis.

## Features

- Track foreground application and window title at a configurable interval.
- Use an editable whitelist to help classify study activity.
- Optionally detect face presence locally through a camera.
- Optionally analyze a screen image with GLM-4.6V-Flash when an API key is configured.
- Review and edit activity records, view summaries, and export CSV.
- Configure sampling, debug logging, and automatic tracking on startup.

## Development

Requirements: Go, Node.js/npm, and the Wails CLI.

```powershell
wails dev
```

Build a Windows executable:

```powershell
wails build
```

The Wails output is written under `build/bin/`. Local settings, records, and logs are stored in the user application data directory.

See [REQUIREMENTS.md](REQUIREMENTS.md) for the detailed product requirements and [CHANGELOG.md](CHANGELOG.md) for release history.
