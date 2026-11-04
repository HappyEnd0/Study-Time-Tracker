# Changelog

All notable changes to **学习时长统计 (Study Time Tracker)** are documented here.

## [1.0.0] - 2026-11-04

### Added

- Windows desktop app built with Go, Wails, Vue 3, and TypeScript.
- Foreground window sampling with an editable study app and website whitelist.
- Configurable sampling intervals for foreground windows, camera, and optional AI analysis.
- Local SQLite activity records, status editing, summaries, and CSV export.
- Optional local face presence detection through the camera.
- Optional GLM-4.6V-Flash image analysis, API key configuration, and a daily call limit.
- Debug mode with an in-app log viewer.
- Setting to automatically begin tracking when the app starts.
- Local-first settings and data storage.

### Notes

- AI image analysis requires a provider API key and is skipped when unavailable or when a request fails.
- Camera detection checks for face presence only; it does not identify people or estimate head pose.
- Learning status is inferred from foreground app/window information and optional camera and AI signals. Review and adjust records when the inference is wrong.
