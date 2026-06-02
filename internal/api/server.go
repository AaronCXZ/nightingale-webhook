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
	mux.HandleFunc("PUT /api/v1/schedule/entry", svr.handleScheduleEntryUpdate)
	mux.HandleFunc("DELETE /api/v1/schedule/entry", svr.handleScheduleEntryDelete)
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

// --- 值班表导入 ---

func (s *Server) handleScheduleImport(w http.ResponseWriter, r *http.Request) {
	// 限制上传大小 10MB
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

	entries, err := schedule.ParseCSV(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	n, err := s.store.ImportSchedule(entries)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported": n,
		"total":    len(entries),
	})
}

// --- 今日值班 ---

func (s *Server) handleScheduleToday(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.GetTodaySchedules()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []store.OncallEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// --- 编辑值班表 ---

type scheduleEntryRequest struct {
	GroupName    string `json:"group_name"`
	Date         string `json:"date"`
	PrimaryName  string `json:"primary_name"`
	PrimaryPhone string `json:"primary_phone"`
	BackupName   string `json:"backup_name"`
	BackupPhone  string `json:"backup_phone"`
}

func (s *Server) handleScheduleEntryUpdate(w http.ResponseWriter, r *http.Request) {
	var req scheduleEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体失败: %v", err)})
		return
	}

	if req.GroupName == "" || req.Date == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group_name 和 date 不能为空"})
		return
	}

	if err := schedule.ValidateDateNotPast(req.Date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// 获取旧值用于日志
	oldEntry, _ := s.store.GetOncallByDate(req.GroupName, req.Date)

	entry := store.OncallEntry{
		GroupName:    req.GroupName,
		Date:         req.Date,
		PrimaryName:  req.PrimaryName,
		PrimaryPhone: req.PrimaryPhone,
		BackupName:   req.BackupName,
		BackupPhone:  req.BackupPhone,
	}

	if err := s.store.UpdateScheduleEntry(req.GroupName, req.Date, entry); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// 记录编辑日志
	if oldEntry != nil {
		logChanges(oldEntry, &entry, s.store)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func logChanges(old *store.OncallEntry, new *store.OncallEntry, s store.Store) {
	change := func(field, oldVal, newVal string) {
		if oldVal != newVal {
			s.LogScheduleChange(store.ScheduleChange{
				GroupName: old.GroupName,
				Date:      old.Date,
				Field:     field,
				OldValue:  oldVal,
				NewValue:  newVal,
			})
		}
	}
	change("primary_name", old.PrimaryName, new.PrimaryName)
	change("primary_phone", old.PrimaryPhone, new.PrimaryPhone)
	change("backup_name", old.BackupName, new.BackupName)
	change("backup_phone", old.BackupPhone, new.BackupPhone)
}

func (s *Server) handleScheduleEntryDelete(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")
	date := r.URL.Query().Get("date")

	if group == "" || date == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group 和 date 参数不能为空"})
		return
	}

	if err := schedule.ValidateDateNotPast(date); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := s.store.DeleteScheduleEntry(group, date); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.store.LogScheduleChange(store.ScheduleChange{
		GroupName: group,
		Date:      date,
		Field:     "deleted",
		OldValue:  "entry",
		NewValue:  "",
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- 导出值班表 ---

func (s *Server) handleScheduleExport(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")

	var startDate, endDate string
	if month != "" {
		// month=2026-06 格式
		startDate = month + "-01"
		// 计算月末
		t, err := time.Parse("2006-01-02", startDate)
		if err == nil {
			endDate = t.AddDate(0, 1, -1).Format("2006-01-02")
		}
	} else {
		startDate = start
		endDate = end
	}

	entries, err := s.store.ExportSchedule(startDate, endDate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	data, err := schedule.ExportCSV(entries)
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
