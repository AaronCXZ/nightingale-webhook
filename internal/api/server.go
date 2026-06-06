package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"webhook/internal/config"
	"webhook/internal/schedule"
	"webhook/internal/store"
	"webhook/internal/webhook"
)

//go:embed dashboard.html
var dashboardHTML string

// Server HTTP 服务器
type Server struct {
	srv     *http.Server
	store   store.Store
	handler *webhook.Handler
	cfg     *config.Config
}

// NewServer 创建 HTTP 服务器
func NewServer(cfg *config.Config, s store.Store, wh *webhook.Handler) *Server {
	svr := &Server{
		store:   s,
		handler: wh,
		cfg:     cfg,
	}

	mux := http.NewServeMux()

	// API 路由
	mux.HandleFunc("POST /api/v1/nightingale/webhook", wh.HandleWebhook)
	mux.HandleFunc("GET /api/v1/health", svr.handleHealth)
	mux.HandleFunc("GET /api/v1/calls", svr.handleCalls)
	mux.HandleFunc("GET /api/v1/stats", svr.handleStats)
	mux.HandleFunc("GET /api/v1/cooldowns", svr.handleCooldowns)

	// 值班表 API
	mux.HandleFunc("POST /api/v1/schedule/import", svr.handleScheduleImport)
	mux.HandleFunc("GET /api/v1/schedule/today", svr.handleScheduleToday)
	mux.HandleFunc("PUT /api/v1/schedule/primary", svr.handleSchedulePrimaryUpdate)
	mux.HandleFunc("PUT /api/v1/schedule/backup", svr.handleScheduleBackupUpdate)
	mux.HandleFunc("DELETE /api/v1/schedule/primary", svr.handleSchedulePrimaryDelete)
	mux.HandleFunc("DELETE /api/v1/schedule/backup", svr.handleScheduleBackupDelete)
	mux.HandleFunc("GET /api/v1/schedule/calendar", svr.handleScheduleCalendar)
	mux.HandleFunc("GET /api/v1/schedule/export", svr.handleScheduleExport)
	mux.HandleFunc("GET /api/v1/schedule/changes", svr.handleScheduleChanges)

	// 监控面板
	mux.HandleFunc("GET /", svr.handleDashboard)

	// 中间件链
	handler := chainMiddleware(mux,
		recoveryMiddleware,
		requestIDMiddleware,
		loggingMiddleware,
		corsMiddleware,
	)

	svr.srv = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return svr
}

// Start 启动 HTTP 服务器（阻塞）
func (s *Server) Start() error {
	slog.Info("server starting", "port", s.cfg.Server.Port, "dry_run", s.cfg.DryRun)
	return s.srv.ListenAndServe()
}

// Shutdown 优雅关闭
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("server shutting down...")
	return s.srv.Shutdown(ctx)
}

// --- 健康检查 ---

type healthResponse struct {
	Status string `json:"status"`
	DryRun bool   `json:"dry_run"`
	Uptime string `json:"uptime"`
}

var startTime = time.Now()

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status: "ok",
		DryRun: s.cfg.DryRun,
		Uptime: time.Since(startTime).Truncate(time.Second).String(),
	})
}

// --- 呼叫历史 ---

func (s *Server) handleCalls(w http.ResponseWriter, r *http.Request) {
	limit := parseIntQuery(r, "limit", 50)
	if limit > 500 {
		limit = 500
	}
	offset := parseIntQuery(r, "offset", 0)

	records, err := s.store.GetRecords(limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if records == nil {
		records = []store.CallRecord{}
	}
	writeJSON(w, http.StatusOK, records)
}

// --- 统计 ---

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// --- 冷却状态 ---

func (s *Server) handleCooldowns(w http.ResponseWriter, r *http.Request) {
	cooldowns, err := s.store.GetCooldowns()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if cooldowns == nil {
		cooldowns = []store.CooldownInfo{}
	}
	writeJSON(w, http.StatusOK, cooldowns)
}

// --- 值班表导入（新 CSV 格式：type,date,group_name,name,phone） ---

func (s *Server) handleScheduleImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析上传文件失败: %v", err)})
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("读取上传文件失败: %v", err)})
		return
	}
	defer file.Close()

	primaries, backups, err := schedule.ParseCSV(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	pCount, bCount, err := s.store.ImportSchedule(primaries, backups)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported_primary": pCount,
		"imported_backup":  bCount,
		"total":            pCount + bCount,
	})
}

// --- 今日值班 ---

func (s *Server) handleScheduleToday(w http.ResponseWriter, r *http.Request) {
	day, err := s.store.GetTodaySchedules()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if day == nil {
		writeJSON(w, http.StatusOK, map[string]string{"message": "今日无值班安排"})
		return
	}
	writeJSON(w, http.StatusOK, day)
}

// --- 修改主值班人 ---

type schedulePrimaryRequest struct {
	Date  string `json:"date"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

func (s *Server) handleSchedulePrimaryUpdate(w http.ResponseWriter, r *http.Request) {
	var req schedulePrimaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体失败: %v", err)})
		return
	}
	if req.Date == "" || req.Phone == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date 和 phone 不能为空"})
		return
	}
	if err := schedule.ValidateDateNotPast(req.Date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.UpsertPrimary(store.OncallPrimary{Date: req.Date, Name: req.Name, Phone: req.Phone}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- 修改备值班人 ---

type scheduleBackupRequest struct {
	Date      string `json:"date"`
	GroupName string `json:"group_name"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`
}

func (s *Server) handleScheduleBackupUpdate(w http.ResponseWriter, r *http.Request) {
	var req scheduleBackupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体失败: %v", err)})
		return
	}
	if req.Date == "" || req.GroupName == "" || req.Phone == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date, group_name, phone 不能为空"})
		return
	}
	if err := schedule.ValidateDateNotPast(req.Date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.UpsertBackup(store.OncallBackup{Date: req.Date, GroupName: req.GroupName, Name: req.Name, Phone: req.Phone}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- 删除主值班人 ---

func (s *Server) handleSchedulePrimaryDelete(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date 参数不能为空"})
		return
	}
	if err := schedule.ValidateDateNotPast(date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.DeletePrimary(date); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- 删除备值班人 ---

func (s *Server) handleScheduleBackupDelete(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	group := r.URL.Query().Get("group")
	if date == "" || group == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date 和 group 参数不能为空"})
		return
	}
	if err := schedule.ValidateDateNotPast(date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.DeleteBackup(date, group); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- 日历数据（JSON，供前端渲染） ---

func (s *Server) handleScheduleCalendar(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}

	days, err := s.store.GetCalendarMonth(month)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if days == nil {
		days = []store.CalendarDay{}
	}
	writeJSON(w, http.StatusOK, days)
}

// --- 导出值班表 CSV（新格式） ---

func (s *Server) handleScheduleExport(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")

	var monthVal string
	if month != "" {
		monthVal = month
	} else if start != "" && end != "" {
		t, err := time.Parse("2006-01-02", start)
		if err == nil {
			// 用起始日期所在月
			monthVal = t.Format("2006-01")
		}
	}
	if monthVal == "" {
		monthVal = time.Now().UTC().Format("2006-01")
	}

	days, err := s.store.GetCalendarMonth(monthVal)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	data, err := schedule.ExportCSV(days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=schedule_%s.csv", time.Now().Format("20060102")))
	w.Write(data)
}

// --- 编辑日志 ---

func (s *Server) handleScheduleChanges(w http.ResponseWriter, r *http.Request) {
	limit := parseIntQuery(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}

	changes, err := s.store.GetScheduleChanges(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if changes == nil {
		changes = []store.ScheduleChange{}
	}
	writeJSON(w, http.StatusOK, changes)
}

// --- 监控面板 ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

// --- 工具函数 ---

func parseIntQuery(r *http.Request, key string, defaultVal int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultVal
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteJSONBody reads YAML json body with reader
var _ io.Reader
