package main

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"image"
	_ "image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pigo "github.com/esimov/pigo/core"
	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite"
)

//go:embed assets/models/facefinder
var faceCascade []byte

type Settings struct {
	WindowIntervalMS  int      `json:"windowIntervalMs"`
	CameraIntervalMS  int      `json:"cameraIntervalMs"`
	AIIntervalSec     int      `json:"aiIntervalSec"`
	CameraEnabled     bool     `json:"cameraEnabled"`
	AIEnabled         bool     `json:"aiEnabled"`
	CapturesEnabled   bool     `json:"capturesEnabled"`
	StudyApps         []string `json:"studyApps"`
	APIKey            string   `json:"apiKey"`
	MaxAICallsDay     int      `json:"maxAICallsDay"`
	DebugEnabled      bool     `json:"debugEnabled"`
	AutoStartTracking bool     `json:"autoStartTracking"`
}

type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
type Segment struct {
	ID          int64  `json:"id"`
	Start       string `json:"start"`
	End         string `json:"end"`
	State       string `json:"state"`
	Evidence    string `json:"evidence"`
	CapturePath string `json:"capturePath"`
}

type App struct {
	ctx                context.Context
	mu                 sync.Mutex
	settings           Settings
	configPath         string
	logPath            string
	logs               []LogEntry
	startedAt          time.Time
	active             bool
	db                 *sql.DB
	trackCancel        context.CancelFunc
	trackContext       context.Context
	segmentStart       time.Time
	segmentState       string
	segmentCapturePath string
	foreground         string
	process            string
	lastAIAnalysis     time.Time
	lastAIResult       string
	aiState            string
	aiStateAt          time.Time
	faceDetector       *pigo.Pigo
	cameraAvailable    bool
	facePresent        bool
	noFaceSince        time.Time
	cameraError        string
}

func NewApp() (*App, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "StudyTracker")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	a := &App{configPath: filepath.Join(dir, "settings.json"), logPath: filepath.Join(dir, "app.log")}
	db, err := sql.Open("sqlite", filepath.Join(dir, "study.db"))
	if err != nil {
		return nil, err
	}
	a.db = db
	detector, detectorErr := pigo.NewPigo().Unpack(faceCascade)
	if detectorErr != nil {
		_ = db.Close()
		return nil, detectorErr
	}
	a.faceDetector = detector
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS time_segments (id INTEGER PRIMARY KEY AUTOINCREMENT,start_time TEXT NOT NULL,end_time TEXT NOT NULL,state TEXT NOT NULL,evidence TEXT NOT NULL DEFAULT '',capture_path TEXT NOT NULL DEFAULT ''); CREATE TABLE IF NOT EXISTS window_usage(id INTEGER PRIMARY KEY AUTOINCREMENT,start_time TEXT NOT NULL,end_time TEXT NOT NULL,process TEXT,title TEXT,state TEXT); CREATE TABLE IF NOT EXISTS segment_edits(id INTEGER PRIMARY KEY AUTOINCREMENT,segment_id INTEGER NOT NULL,old_state TEXT NOT NULL,new_state TEXT NOT NULL,edited_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS ai_usage(date TEXT PRIMARY KEY,calls INTEGER NOT NULL DEFAULT 0)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	columns, err := db.Query(`PRAGMA table_info(time_segments)`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	hasCapturePath := false
	for columns.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err = columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			columns.Close()
			_ = db.Close()
			return nil, err
		}
		if name == "capture_path" {
			hasCapturePath = true
		}
	}
	if err = columns.Err(); err != nil {
		columns.Close()
		_ = db.Close()
		return nil, err
	}
	columns.Close()
	if !hasCapturePath {
		if _, err = db.Exec(`ALTER TABLE time_segments ADD COLUMN capture_path TEXT NOT NULL DEFAULT ''`); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	a.settings = Settings{WindowIntervalMS: 1000, CameraIntervalMS: 500, AIIntervalSec: 30, MaxAICallsDay: 1800, CameraEnabled: false, AIEnabled: false, CapturesEnabled: false, StudyApps: []string{}}
	if b, e := os.ReadFile(a.configPath); e == nil {
		_ = json.Unmarshal(b, &a.settings)
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	// Camera access belongs to the current app session; never show a saved
	// preference as active when no browser camera stream exists after restart.
	a.settings.CameraEnabled = false
	if key, e := keyring.Get("study-tracker", "glm-api-key"); e == nil {
		a.settings.APIKey = key
	}
	if a.settings.DebugEnabled {
		if b, e := os.ReadFile(a.logPath); e == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if line == "" {
					continue
				}
				left, msg, ok := strings.Cut(line, "] ")
				if !ok {
					continue
				}
				stamp, level, ok := strings.Cut(left, " [")
				if !ok {
					continue
				}
				a.logs = append(a.logs, LogEntry{Time: stamp, Level: level, Message: strings.TrimRight(msg, "\r")})
			}
			if len(a.logs) > 1000 {
				a.logs = append([]LogEntry(nil), a.logs[len(a.logs)-1000:]...)
			}
		}
	}
	a.log("INFO", "应用已初始化")
	return a, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.log("INFO", "桌面窗口启动")
	a.mu.Lock()
	autoStart := a.settings.AutoStartTracking
	a.mu.Unlock()
	if autoStart {
		a.StartTracking()
	}
}
func (a *App) shutdown(ctx context.Context) {
	a.StopTracking()
	a.log("INFO", "应用正在退出")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		_ = a.db.Close()
	}
}
func (a *App) GetSettings() Settings { a.mu.Lock(); defer a.mu.Unlock(); return a.settings }
func (a *App) GetLogs() []LogEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.settings.DebugEnabled {
		return []LogEntry{}
	}
	copyOfLogs := append([]LogEntry(nil), a.logs...)
	return copyOfLogs
}
func (a *App) ClearLogs() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = nil
	if err := os.WriteFile(a.logPath, nil, 0600); err != nil {
		return err
	}
	a.logLocked("INFO", "日志已清空")
	return nil
}

func (a *App) log(level, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.settings.DebugEnabled && level == "DEBUG" {
		return
	}
	a.logLocked(level, message)
}
func (a *App) logLocked(level, message string) {
	if !a.settings.DebugEnabled {
		return
	}
	entry := LogEntry{Time: time.Now().Format(time.RFC3339), Level: level, Message: message}
	a.logs = append(a.logs, entry)
	if len(a.logs) > 1000 {
		a.logs = append([]LogEntry(nil), a.logs[len(a.logs)-1000:]...)
	}
	line := fmt.Sprintf("%s [%s] %s\n", entry.Time, entry.Level, entry.Message)
	if f, err := os.OpenFile(a.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
}

func (a *App) SaveSettings(s Settings) error {
	if s.WindowIntervalMS < 250 || s.WindowIntervalMS > 10000 {
		return errors.New("窗口采样间隔须为 250–10000 毫秒")
	}
	if s.CameraIntervalMS < 100 || s.CameraIntervalMS > 5000 {
		return errors.New("摄像头采样间隔须为 100–5000 毫秒")
	}
	if s.AIIntervalSec < 10 || s.AIIntervalSec > 3600 {
		return errors.New("AI 分析间隔须为 10–3600 秒")
	}
	if s.MaxAICallsDay < 1 || s.MaxAICallsDay > 2000 {
		return errors.New("每日 AI 调用上限须为 1–2000 次")
	}
	if s.APIKey != "" {
		if err := keyring.Set("study-tracker", "glm-api-key", s.APIKey); err != nil {
			return errors.New("无法安全保存 API Key：" + err.Error())
		}
	} else {
		_ = keyring.Delete("study-tracker", "glm-api-key")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	persisted := s
	persisted.APIKey = ""
	b, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.configPath + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, a.configPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	a.settings = s
	if !s.AIEnabled {
		a.aiState = ""
		a.aiStateAt = time.Time{}
		a.lastAIResult = ""
		a.lastAIAnalysis = time.Time{}
	}
	a.logLocked("INFO", "设置已保存")
	return nil
}

func (a *App) StartTracking() map[string]any {
	a.mu.Lock()
	if !a.active {
		a.active = true
		a.startedAt = time.Now()
		a.segmentStart = a.startedAt
		a.trackContext, a.trackCancel = context.WithCancel(context.Background())
		go a.trackLoop(a.trackContext)
	}
	a.logLocked("INFO", "开始学习记录")
	status := a.statusLocked()
	a.mu.Unlock()
	return status
}
func (a *App) StopTracking() map[string]any {
	a.mu.Lock()
	wasActive := a.active
	a.active = false
	if a.trackCancel != nil {
		a.trackCancel()
		a.trackCancel = nil
	}
	if wasActive {
		a.flushLocked()
		a.logLocked("INFO", "暂停学习记录")
	}
	status := a.statusLocked()
	a.mu.Unlock()
	return status
}
func (a *App) Status() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked()
}
func (a *App) statusLocked() map[string]any {
	var elapsed int64
	if a.active {
		elapsed = int64(time.Since(a.startedAt).Seconds())
	}
	return map[string]any{"active": a.active, "state": a.segmentState, "elapsedSeconds": elapsed, "windowTitle": a.foreground, "processName": a.process, "today": a.todaySummaryLocked(), "aiResult": a.lastAIResult, "cameraAvailable": a.cameraAvailable, "facePresent": a.facePresent, "cameraError": a.cameraError}
}
func (a *App) flushLocked() {
	if a.db == nil || a.segmentStart.IsZero() || !a.segmentStart.Before(time.Now()) {
		return
	}
	end := time.Now()
	state := a.segmentState
	if state == "" {
		state = "unknown"
	}
	start := a.segmentStart
	capturePath := a.segmentCapturePath
	for start.Before(end) {
		midnight := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, start.Location())
		partEnd := end
		if midnight.Before(partEnd) {
			partEnd = midnight
		}
		_, err := a.db.Exec(`INSERT INTO time_segments(start_time,end_time,state,evidence,capture_path) VALUES(?,?,?,?,?)`, start.Format(time.RFC3339Nano), partEnd.Format(time.RFC3339Nano), state, a.process+" | "+a.foreground, capturePath)
		if err != nil {
			a.logLocked("ERROR", "保存时间片失败: "+err.Error())
			return
		}
		capturePath = ""
		start = partEnd
	}
	a.segmentStart = end
	a.segmentCapturePath = ""
}
func (a *App) trackLoop(ctx context.Context) {
	for {
		a.mu.Lock()
		interval := time.Duration(a.settings.WindowIntervalMS) * time.Millisecond
		a.mu.Unlock()
		ticker := time.NewTicker(interval)
		select {
		case <-ctx.Done():
			ticker.Stop()
			return
		case <-ticker.C:
		}
		ticker.Stop()
		proc, title, err := foregroundWindow()
		captureStart := time.Time{}
		captureState := ""
		a.mu.Lock()
		if !a.active {
			a.mu.Unlock()
			return
		}
		if err != nil {
			a.logLocked("ERROR", "读取前台窗口失败: "+err.Error())
		} else {
			state := "slacking"
			for _, name := range a.settings.StudyApps {
				if name != "" && (strings.EqualFold(name, proc) || strings.Contains(strings.ToLower(title), strings.ToLower(name))) {
					state = "studying"
					break
				}
			}
			if a.settings.AIEnabled && a.aiState != "" && time.Since(a.aiStateAt) < time.Duration(a.settings.AIIntervalSec*2)*time.Second {
				state = a.aiState
			}
			if a.settings.CameraEnabled && a.cameraAvailable && !a.facePresent && !a.noFaceSince.IsZero() && time.Since(a.noFaceSince) >= 8*time.Second {
				state = "away"
			}
			if state != a.segmentState {
				a.flushLocked()
				a.segmentStart = time.Now()
				a.segmentState = state
				a.segmentCapturePath = ""
				if a.settings.CapturesEnabled {
					captureStart = a.segmentStart
					captureState = state
				}
				a.logLocked("DEBUG", "状态变化为 "+state)
			}
			a.process = proc
			a.foreground = title
		}
		if time.Since(a.segmentStart) >= 30*time.Second {
			a.flushLocked()
		}
		shouldAnalyze := a.settings.AIEnabled && a.settings.APIKey != "" && time.Since(a.lastAIAnalysis) >= time.Duration(a.settings.AIIntervalSec)*time.Second
		if shouldAnalyze {
			a.lastAIAnalysis = time.Now()
		}
		a.mu.Unlock()
		if captureState != "" {
			go a.captureSegmentSnapshot(captureState, captureStart)
		}
		if shouldAnalyze {
			go a.analyzeForeground()
		}
	}
}

func (a *App) captureSegmentSnapshot(state string, segmentStart time.Time) {
	img, err := captureActiveWindowJPEG()
	if err != nil {
		a.log("ERROR", "状态截图失败: "+err.Error())
		return
	}
	dir := filepath.Join(filepath.Dir(a.configPath), "captures")
	if err = os.MkdirAll(dir, 0700); err != nil {
		a.log("ERROR", "创建截图目录失败: "+err.Error())
		return
	}
	path := filepath.Join(dir, segmentStart.Format("20060102-150405.000")+"-"+state+".jpg")
	if err = os.WriteFile(path, img, 0600); err != nil {
		a.log("ERROR", "保存状态截图失败: "+err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	result, err := a.db.Exec(`UPDATE time_segments SET capture_path=? WHERE start_time=? AND state=? AND capture_path=''`, path, segmentStart.Format(time.RFC3339Nano), state)
	if err != nil {
		_ = os.Remove(path)
		a.logLocked("ERROR", "关联状态截图失败: "+err.Error())
		return
	}
	if changed, _ := result.RowsAffected(); changed > 0 {
		a.logLocked("INFO", "状态截图已保存")
		return
	}
	if a.active && a.segmentStart.Equal(segmentStart) && a.segmentState == state && a.settings.CapturesEnabled {
		a.segmentCapturePath = path
		a.logLocked("INFO", "状态截图已保存")
		return
	}
	_ = os.Remove(path)
}
func (a *App) analyzeForeground() {
	img, err := captureActiveWindowJPEG()
	if err != nil {
		a.log("ERROR", "截图采集失败: "+err.Error())
		return
	}
	result, err := a.AnalyzeImage("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img))
	if err != nil {
		a.log("ERROR", "AI 图片分析失败: "+err.Error())
		return
	}
	text, _ := result["analysis"].(string)
	a.mu.Lock()
	if !a.active || !a.settings.AIEnabled {
		a.mu.Unlock()
		return
	}
	a.lastAIResult = text
	newState := ""
	if strings.Contains(text, "非学习") {
		newState = "slacking"
	} else if strings.Contains(text, "学习中") {
		newState = "studying"
	}
	a.aiState = newState
	a.aiStateAt = time.Now()
	a.logLocked("INFO", "前台截图 AI 分析完成")
	a.mu.Unlock()
}
func (a *App) TodaySummary() map[string]int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.todaySummaryLocked()
}
func (a *App) GetSegments(days int) []Segment {
	a.mu.Lock()
	defer a.mu.Unlock()
	if days < 1 || days > 3650 {
		days = 30
	}
	cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	rows, err := a.db.Query(`SELECT id,start_time,end_time,state,evidence,capture_path FROM time_segments WHERE substr(start_time,1,10)>=? ORDER BY start_time DESC LIMIT 1000`, cutoff)
	if err != nil {
		return []Segment{}
	}
	defer rows.Close()
	out := []Segment{}
	for rows.Next() {
		var x Segment
		if rows.Scan(&x.ID, &x.Start, &x.End, &x.State, &x.Evidence, &x.CapturePath) == nil {
			out = append(out, x)
		}
	}
	return out
}

func (a *App) GetSegmentCapture(id int64) (string, error) {
	a.mu.Lock()
	var path string
	err := a.db.QueryRow(`SELECT capture_path FROM time_segments WHERE id=?`, id).Scan(&path)
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("这条记录没有截图")
	}
	if !a.isCapturePath(path) {
		return "", errors.New("截图路径无效")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (a *App) DeleteSegmentCapture(id int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var path string
	if err := a.db.QueryRow(`SELECT capture_path FROM time_segments WHERE id=?`, id).Scan(&path); err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	if !a.isCapturePath(path) {
		return errors.New("截图路径无效")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := a.db.Exec(`UPDATE time_segments SET capture_path='' WHERE id=?`, id)
	return err
}

func (a *App) isCapturePath(path string) bool {
	base, err := filepath.Abs(filepath.Join(filepath.Dir(a.configPath), "captures"))
	if err != nil {
		return false
	}
	full, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, full)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (a *App) EditSegment(id int64, state string) error {
	if state != "studying" && state != "slacking" && state != "away" && state != "unknown" {
		return errors.New("无效状态")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	if err = tx.QueryRow(`SELECT state FROM time_segments WHERE id=?`, id).Scan(&old); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO segment_edits(segment_id,old_state,new_state,edited_at) VALUES(?,?,?,?)`, id, old, state, time.Now().Format(time.RFC3339))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE time_segments SET state=? WHERE id=?`, state, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (a *App) GetDailyStats(days int) []map[string]any {
	if days != 7 && days != 30 {
		days = 7
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []map[string]any{}
	for i := days - 1; i >= 0; i-- {
		date := time.Now().AddDate(0, 0, -i)
		key := date.Format("2006-01-02")
		s := map[string]int64{"studying": 0, "slacking": 0, "away": 0, "unknown": 0}
		rows, err := a.db.Query(`SELECT state,start_time,end_time FROM time_segments WHERE substr(start_time,1,10)=?`, key)
		if err == nil {
			for rows.Next() {
				var stt, sta, end string
				if rows.Scan(&sta, &stt, &end) == nil {
					t1, e1 := time.Parse(time.RFC3339Nano, stt)
					t2, e2 := time.Parse(time.RFC3339Nano, end)
					if e1 == nil && e2 == nil {
						s[sta] += int64(t2.Sub(t1).Seconds())
					}
				}
			}
			rows.Close()
		}
		out = append(out, map[string]any{"date": key, "studying": s["studying"], "slacking": s["slacking"], "away": s["away"], "unknown": s["unknown"]})
	}
	return out
}
func (a *App) ExportCSV(days int) (string, error) {
	if a.ctx == nil {
		return "", errors.New("应用尚未启动")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{DefaultFilename: "study-records.csv", Filters: []runtime.FileFilter{{DisplayName: "CSV 文件", Pattern: "*.csv"}}})
	if err != nil || path == "" {
		return path, err
	}
	rows := a.GetSegments(days)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"开始时间", "结束时间", "分类", "证据", "时长秒"})
	for _, x := range rows {
		t1, e1 := time.Parse(time.RFC3339Nano, x.Start)
		t2, e2 := time.Parse(time.RFC3339Nano, x.End)
		if e1 != nil || e2 != nil {
			continue
		}
		_ = w.Write([]string{x.Start, x.End, x.State, x.Evidence, fmt.Sprint(int64(t2.Sub(t1).Seconds()))})
	}
	w.Flush()
	return path, w.Error()
}

func (a *App) AnalyzeImage(dataURL string) (map[string]any, error) {
	image := dataURL
	mimeType := "image/jpeg"
	if i := strings.Index(image, ","); i >= 0 {
		header := image[:i]
		image = image[i+1:]
		if strings.HasPrefix(header, "data:") {
			mimeType = strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		}
	}
	if mimeType != "image/jpeg" && mimeType != "image/png" && mimeType != "image/webp" {
		return nil, errors.New("图片格式无效，请使用 JPEG、PNG 或 WebP")
	}
	if len(image) > 8*1024*1024 {
		return nil, errors.New("图片过大，请选用小于 6 MB 的图片")
	}
	if _, err := base64.StdEncoding.DecodeString(image); err != nil {
		return nil, errors.New("图片格式无效")
	}

	a.mu.Lock()
	if !a.settings.AIEnabled || strings.TrimSpace(a.settings.APIKey) == "" {
		a.mu.Unlock()
		return nil, errors.New("请先在设置中启用 AI 并填写 API Key")
	}
	limit := a.settings.MaxAICallsDay
	if limit < 1 || limit > 2000 {
		limit = 1800
	}
	day := time.Now().Format("2006-01-02")
	var used int
	_ = a.db.QueryRow(`SELECT calls FROM ai_usage WHERE date=?`, day).Scan(&used)
	if used >= limit {
		a.mu.Unlock()
		return nil, fmt.Errorf("今日 AI 分析额度已达上限（%d 次）", limit)
	}
	key := a.settings.APIKey
	_, err := a.db.Exec(`INSERT INTO ai_usage(date,calls) VALUES(?,1) ON CONFLICT(date) DO UPDATE SET calls=calls+1`, day)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": "glm-4.6v-flash", "max_tokens": 160, "temperature": 0.1, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "判断这张屏幕图片是否主要展示学习、工作或知识内容。只用一句话简述判断依据，并在末尾给出标签：学习中、非学习或不确定。不要推断图片之外的信息。"}, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + mimeType + ";base64," + image}}}}}}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, "https://open.bigmodel.cn/api/paas/v4/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("AI 服务返回 HTTP %d", resp.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, errors.New("AI 未返回分析结果")
	}
	return map[string]any{"analysis": result.Choices[0].Message.Content, "callsUsed": used + 1, "callsLimit": limit}, nil
}

func (a *App) AIUsage() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	var used int
	_ = a.db.QueryRow(`SELECT calls FROM ai_usage WHERE date=?`, time.Now().Format("2006-01-02")).Scan(&used)
	limit := a.settings.MaxAICallsDay
	if limit < 1 {
		limit = 1800
	}
	return map[string]int{"used": used, "limit": limit}
}

func (a *App) DetectFace(dataURL string) (bool, error) {
	_, payload, ok := strings.Cut(dataURL, ",")
	if !ok {
		payload = dataURL
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return false, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return false, err
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return false, errors.New("空画面")
	}
	pixels := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			pixels[y*w+x] = uint8((299*r + 587*g + 114*b) / 1000 >> 8)
		}
	}
	a.mu.Lock()
	detector := a.faceDetector
	a.mu.Unlock()
	if detector == nil {
		return false, errors.New("人脸检测器未初始化")
	}
	params := pigo.CascadeParams{MinSize: 32, MaxSize: min(w, h), ShiftFactor: 0.12, ScaleFactor: 1.12, ImageParams: pigo.ImageParams{Pixels: pixels, Rows: h, Cols: w, Dim: w}}
	detections := detector.RunCascade(params, 0)
	detections = detector.ClusterDetections(detections, 0.2)
	for _, detection := range detections {
		if detection.Q > 5.0 {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) SetCameraSignal(present bool, errorText string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cameraError = errorText
	if errorText == "摄像头已关闭" {
		a.cameraAvailable = false
		a.facePresent = false
		a.noFaceSince = time.Time{}
		a.cameraError = ""
		return
	}
	if errorText != "" {
		a.cameraAvailable = false
		a.facePresent = false
		a.noFaceSince = time.Time{}
		a.logLocked("ERROR", "摄像头检测不可用: "+errorText)
		return
	}
	a.cameraAvailable = true
	a.facePresent = present
	if present {
		a.noFaceSince = time.Time{}
	} else if a.noFaceSince.IsZero() {
		a.noFaceSince = time.Now()
	}
}
func (a *App) todaySummaryLocked() map[string]int64 {
	out := map[string]int64{"studying": 0, "slacking": 0, "away": 0, "unknown": 0}
	if a.db == nil {
		return out
	}
	day := time.Now().Format("2006-01-02")
	rows, err := a.db.Query(`SELECT state,start_time,end_time FROM time_segments WHERE substr(start_time,1,10)=?`, day)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var state, start, end string
		if rows.Scan(&state, &start, &end) == nil {
			st, e1 := time.Parse(time.RFC3339Nano, start)
			en, e2 := time.Parse(time.RFC3339Nano, end)
			if e1 == nil && e2 == nil {
				out[state] += int64(en.Sub(st).Seconds())
			}
		}
	}
	if a.active && a.segmentState != "" && !a.segmentStart.IsZero() {
		out[a.segmentState] += int64(time.Since(a.segmentStart).Seconds())
	}
	return out
}
