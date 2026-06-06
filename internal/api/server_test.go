package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"webhook/internal/config"
	"webhook/internal/notifier"
	"webhook/internal/store"
	"webhook/internal/webhook"
)

// --- 测试辅助 ---

func newTestServer(t *testing.T) (*httptest.Server, store.Store) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	caller := &notifier.MockCaller{}
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Alert: config.AlertConfig{
			MinSeverity:        3,
			Cooldown:           15 * time.Minute,
			GroupCooldown:      5 * time.Minute,
			MaxConcurrentCalls: 5,
		},
		Retry: config.RetryConfig{
			MaxAttempts: 3,
			BackoffBase: 1 * time.Second,
		},
		DryRun:  true,
		Storage: config.StorageConfig{DBPath: ":memory:"},
		Logging: config.LoggingConfig{Level: "debug", Format: "text"},
	}

	wh := webhook.New(s, caller, cfg)
	server := NewServer(cfg, s, wh)
	ts := httptest.NewServer(server.srv.Handler)
	t.Cleanup(func() { ts.Close() })
	return ts, s
}

func getJSON(t *testing.T, url string, target interface{}) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("解析 JSON 失败: %v", err)
	}
	return resp
}

func doRequest(t *testing.T, method, url string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	return resp
}

func tomorrow(t *testing.T) string {
	t.Helper()
	return time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
}

// --- parseIntQuery ---

func TestParseIntQueryMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/calls", nil)
	got := parseIntQuery(req, "limit", 50)
	if got != 50 {
		t.Errorf("无参数时应返回默认值 50，got %d", got)
	}
}

func TestParseIntQueryValid(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/calls?limit=10", nil)
	got := parseIntQuery(req, "limit", 50)
	if got != 10 {
		t.Errorf("应返回 10，got %d", got)
	}
}

func TestParseIntQueryNegative(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/calls?limit=-5", nil)
	got := parseIntQuery(req, "limit", 50)
	if got != 50 {
		t.Errorf("负数应返回默认值 50，got %d", got)
	}
}

func TestParseIntQueryInvalid(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/calls?limit=abc", nil)
	got := parseIntQuery(req, "limit", 50)
	if got != 50 {
		t.Errorf("无效值应返回默认值 50，got %d", got)
	}
}

// --- 健康检查 ---

func TestHandleHealth(t *testing.T) {
	ts, _ := newTestServer(t)

	var resp struct {
		Status string `json:"status"`
		DryRun bool   `json:"dry_run"`
		Uptime string `json:"uptime"`
	}
	r := getJSON(t, ts.URL+"/api/v1/health", &resp)

	if r.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", r.StatusCode)
	}
	if resp.Status != "ok" {
		t.Errorf("status 应为 'ok'，got %q", resp.Status)
	}
	if !resp.DryRun {
		t.Error("dry_run 应为 true")
	}
	if resp.Uptime == "" {
		t.Error("uptime 不应为空")
	}
}

// --- 呼叫历史 ---

func TestHandleCallsEmpty(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/calls")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}

	var records []store.CallRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		t.Fatalf("解析 JSON 失败: %v", err)
	}
	if records == nil || len(records) != 0 {
		t.Errorf("空数据库应返回空数组，got %v", records)
	}
}

func TestHandleCallsWithData(t *testing.T) {
	ts, s := newTestServer(t)

	// 先插入一条记录
	cid := "test-call-001"
	s.SaveRecord(store.CallRecord{
		GroupName:  "ops",
		AlertName:  "CPU过高",
		AlertHash:  "abc123",
		Severity:   1,
		Phone:      "13800001111",
		CallID:     &cid,
		CallStatus: store.CallStatusInitiated,
		DryRun:     true,
	})

	resp, err := http.Get(ts.URL + "/api/v1/calls?limit=10")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	var records []store.CallRecord
	json.NewDecoder(resp.Body).Decode(&records)

	if len(records) != 1 {
		t.Fatalf("应有 1 条记录，got %d", len(records))
	}
	if records[0].GroupName != "ops" {
		t.Errorf("GroupName 应为 ops，got %s", records[0].GroupName)
	}
	if records[0].CallStatus != store.CallStatusInitiated {
		t.Errorf("CallStatus 应为 %s，got %s", store.CallStatusInitiated, records[0].CallStatus)
	}
}

func TestHandleCallsLimitMax(t *testing.T) {
	ts, _ := newTestServer(t)

	// limit > 500 应被限制为 500
	resp, err := http.Get(ts.URL + "/api/v1/calls?limit=1000")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
}

// --- 统计 ---

func TestHandleStats(t *testing.T) {
	ts, _ := newTestServer(t)

	var stats store.Stats
	r := getJSON(t, ts.URL+"/api/v1/stats", &stats)

	if r.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", r.StatusCode)
	}
	if stats.TotalCalls != 0 {
		t.Errorf("TotalCalls 应为 0，got %d", stats.TotalCalls)
	}
	if stats.ByGroup == nil {
		t.Error("ByGroup 不应为 nil")
	}
	if stats.BySeverity == nil {
		t.Error("BySeverity 不应为 nil")
	}
}

// --- 冷却状态 ---

func TestHandleCooldowns(t *testing.T) {
	ts, _ := newTestServer(t)

	var cooldowns []store.CooldownInfo
	r := getJSON(t, ts.URL+"/api/v1/cooldowns", &cooldowns)

	if r.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", r.StatusCode)
	}
	if cooldowns == nil {
		t.Error("cooldowns 不应为 nil")
	}
	if len(cooldowns) != 0 {
		t.Errorf("空列表应为 0，got %d", len(cooldowns))
	}
}

// --- 仪表盘 ---

func TestHandleDashboard(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Webhook 监控面板") {
		t.Error("页面应包含 'Webhook 监控面板'")
	}
	if !strings.Contains(string(body), "navMonth") {
		t.Error("页面应包含 navMonth 函数")
	}
}

func TestHandleDashboard404(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/nonexistent")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("状态码应为 404，got %d", resp.StatusCode)
	}
}

// --- 日历数据（JSON API）---

func TestHandleScheduleCalendar(t *testing.T) {
	ts, s := newTestServer(t)

	day := tomorrow(t)
	s.ImportSchedule([]store.OncallPrimary{
		{Date: day, Name: "张三", Phone: "13800001111"},
	}, []store.OncallBackup{
		{Date: day, GroupName: "ops", Name: "李四", Phone: "13800002222"},
	})

	var days []store.CalendarDay
	r := getJSON(t, ts.URL+"/api/v1/schedule/calendar?month="+day[:7], &days)

	if r.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", r.StatusCode)
	}
	if len(days) == 0 {
		t.Fatal("应有日历数据")
	}
	// 找到有数据的日期
	var found bool
	for _, d := range days {
		if d.Date == day {
			found = true
			if d.PrimaryName != "张三" {
				t.Errorf("主值应为 张三，got %s", d.PrimaryName)
			}
			break
		}
	}
	if !found {
		t.Error("日历数据中未找到导入的日期")
	}
}

func TestHandleScheduleCalendarDefaultMonth(t *testing.T) {
	ts, _ := newTestServer(t)

	// 无 month 参数，应使用当月
	resp, err := http.Get(ts.URL + "/api/v1/schedule/calendar")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
}

// --- 值班表导出 ---

func TestHandleScheduleExport(t *testing.T) {
	ts, s := newTestServer(t)

	day := tomorrow(t)
	s.ImportSchedule([]store.OncallPrimary{
		{Date: day, Name: "张三", Phone: "13800001111"},
	}, []store.OncallBackup{
		{Date: day, GroupName: "ops", Name: "李四", Phone: "13800002222"},
	})

	month := day[:7]
	resp, err := http.Get(ts.URL + "/api/v1/schedule/export?month=" + month)
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	csv := string(body)
	if !strings.HasPrefix(csv, "type,date") {
		t.Error("CSV 应以 'type,date' header 开头")
	}
	if !strings.Contains(csv, "primary") {
		t.Error("CSV 应包含 primary 行")
	}
	if !strings.Contains(csv, "backup") {
		t.Error("CSV 应包含 backup 行")
	}
}

func TestHandleScheduleExportDefaultMonth(t *testing.T) {
	ts, _ := newTestServer(t)

	// 无 month 参数，使用当月
	resp, err := http.Get(ts.URL + "/api/v1/schedule/export")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
}

// --- 今日值班 ---

func TestHandleScheduleToday(t *testing.T) {
	ts, s := newTestServer(t)

	today := time.Now().UTC().Format("2006-01-02")
	s.ImportSchedule([]store.OncallPrimary{
		{Date: today, Name: "张三", Phone: "13800001111"},
	}, []store.OncallBackup{
		{Date: today, GroupName: "ops", Name: "李四", Phone: "13800002222"},
	})

	var day store.CalendarDay
	r := getJSON(t, ts.URL+"/api/v1/schedule/today", &day)

	if r.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", r.StatusCode)
	}
	if day.PrimaryName != "张三" {
		t.Errorf("主值应为 张三，got %s", day.PrimaryName)
	}
	if len(day.Backups) != 1 {
		t.Errorf("应有 1 个备值，got %d", len(day.Backups))
	}
}

func TestHandleScheduleTodayEmpty(t *testing.T) {
	ts, _ := newTestServer(t)

	var resp map[string]string
	r := getJSON(t, ts.URL+"/api/v1/schedule/today", &resp)

	if r.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", r.StatusCode)
	}
}

// --- 修改主值班人 ---

func TestHandleSchedulePrimaryUpdate(t *testing.T) {
	ts, _ := newTestServer(t)

	day := tomorrow(t)
	body := map[string]string{
		"date":  day,
		"name":  "张三",
		"phone": "13800001111",
	}
	jsonBody, _ := json.Marshal(body)

	resp := doRequest(t, http.MethodPut, ts.URL+"/api/v1/schedule/primary",
		bytes.NewReader(jsonBody), "application/json")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Errorf("status 应为 'ok'，got %q", result["status"])
	}
}

func TestHandleSchedulePrimaryUpdateMissingDate(t *testing.T) {
	ts, _ := newTestServer(t)

	body := map[string]string{
		"date":  "",
		"name":  "张三",
		"phone": "13800001111",
	}
	jsonBody, _ := json.Marshal(body)

	resp := doRequest(t, http.MethodPut, ts.URL+"/api/v1/schedule/primary",
		bytes.NewReader(jsonBody), "application/json")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("空 date 应返回 400，got %d", resp.StatusCode)
	}
}

// --- 修改备值班人 ---

func TestHandleScheduleBackupUpdate(t *testing.T) {
	ts, _ := newTestServer(t)

	day := tomorrow(t)
	body := map[string]string{
		"date":       day,
		"group_name": "ops",
		"name":       "李四",
		"phone":      "13800002222",
	}
	jsonBody, _ := json.Marshal(body)

	resp := doRequest(t, http.MethodPut, ts.URL+"/api/v1/schedule/backup",
		bytes.NewReader(jsonBody), "application/json")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
}

func TestHandleScheduleBackupUpdateMissingGroup(t *testing.T) {
	ts, _ := newTestServer(t)

	body := map[string]string{
		"date":       tomorrow(t),
		"group_name": "",
		"name":       "李四",
		"phone":      "13800002222",
	}
	jsonBody, _ := json.Marshal(body)

	resp := doRequest(t, http.MethodPut, ts.URL+"/api/v1/schedule/backup",
		bytes.NewReader(jsonBody), "application/json")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("空 group_name 应返回 400，got %d", resp.StatusCode)
	}
}

// --- 删除主值班人 ---

func TestHandleSchedulePrimaryDelete(t *testing.T) {
	ts, s := newTestServer(t)

	day := tomorrow(t)
	s.ImportSchedule([]store.OncallPrimary{
		{Date: day, Name: "张三", Phone: "13800001111"},
	}, nil)

	resp := doRequest(t, http.MethodDelete,
		ts.URL+"/api/v1/schedule/primary?date="+day, nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "deleted" {
		t.Errorf("status 应为 'deleted'，got %q", result["status"])
	}
}

func TestHandleSchedulePrimaryDeleteMissingDate(t *testing.T) {
	ts, _ := newTestServer(t)

	resp := doRequest(t, http.MethodDelete, ts.URL+"/api/v1/schedule/primary", nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("缺 date 应返回 400，got %d", resp.StatusCode)
	}
}

// --- 删除备值班人 ---

func TestHandleScheduleBackupDelete(t *testing.T) {
	ts, s := newTestServer(t)

	day := tomorrow(t)
	s.ImportSchedule(nil, []store.OncallBackup{
		{Date: day, GroupName: "ops", Name: "李四", Phone: "13800002222"},
	})

	resp := doRequest(t, http.MethodDelete,
		ts.URL+"/api/v1/schedule/backup?date="+day+"&group=ops", nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
}

func TestHandleScheduleBackupDeleteMissingParam(t *testing.T) {
	ts, _ := newTestServer(t)

	resp := doRequest(t, http.MethodDelete, ts.URL+"/api/v1/schedule/backup", nil, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("缺参数应返回 400，got %d", resp.StatusCode)
	}
}

// --- 编辑日志 ---

func TestHandleScheduleChanges(t *testing.T) {
	ts, s := newTestServer(t)

	s.LogScheduleChange(store.ScheduleChange{
		GroupName: "ops",
		Date:      tomorrow(t),
		Field:     "primary_name",
		OldValue:  "张三",
		NewValue:  "李四",
	})

	var changes []store.ScheduleChange
	r := getJSON(t, ts.URL+"/api/v1/schedule/changes?limit=10", &changes)

	if r.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", r.StatusCode)
	}
	if len(changes) == 0 {
		t.Error("应返回变更日志")
	}
	if changes[0].GroupName != "ops" {
		t.Errorf("GroupName 应为 ops，got %s", changes[0].GroupName)
	}
}

func TestHandleScheduleChangesLimitDefault(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/schedule/changes")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
}

// --- CSV 导入（新格式） ---

func TestHandleScheduleImport(t *testing.T) {
	ts, _ := newTestServer(t)

	day := tomorrow(t)
	csvContent := fmt.Sprintf("type,date,group_name,name,phone\nprimary,%s,main,张三,13800001111\nbackup,%s,ops,李四,13800002222\n", day, day)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.csv")
	fw.Write([]byte(csvContent))
	w.Close()

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/v1/schedule/import",
		&buf, w.FormDataContentType())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["imported_primary"] != float64(1) {
		t.Errorf("应导入 1 条 primary，got %v", result["imported_primary"])
	}
	if result["imported_backup"] != float64(1) {
		t.Errorf("应导入 1 条 backup，got %v", result["imported_backup"])
	}
}

func TestHandleScheduleImportInvalidCSV(t *testing.T) {
	ts, _ := newTestServer(t)

	// 无效 CSV
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.csv")
	fw.Write([]byte("invalid"))
	w.Close()

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/v1/schedule/import",
		&buf, w.FormDataContentType())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("无效 CSV 应返回 400，got %d", resp.StatusCode)
	}
}

// --- 呼叫记录 limit 超限测试 ---

func TestHandleCallsDefaultLimit(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/calls?limit=abc")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	// 无效 limit 使用默认 50，请求仍然成功
	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}
}

// --- webhook 路由 ---

func TestHandleWebhookRoute(t *testing.T) {
	ts, _ := newTestServer(t)

	// POST 到 webhook 路由应成功（dry run 模式）
	body := strings.NewReader(`[{"rule_name":"test","severity":1,"is_recovered":false,"hash":"test-route-001","group_name":"ops"}]`)
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/v1/nightingale/webhook",
		body, "application/json")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", resp.StatusCode)
	}

	var summary webhook.WebhookSummary
	json.NewDecoder(resp.Body).Decode(&summary)
	if summary.Total != 1 {
		t.Errorf("Total 应为 1，got %d", summary.Total)
	}
}

// --- 中间件集成测试（通过测试服务验证） ---

func TestCORSMiddlewareViaServer(t *testing.T) {
	ts, _ := newTestServer(t)

	// OPTIONS 到任意 API 路由
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/v1/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS 请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS 状态码应为 204，got %d", resp.StatusCode)
	}
	if origin := resp.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("Access-Control-Allow-Origin 应为 *，got %q", origin)
	}
}

func TestRequestIDViaServer(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET 失败: %v", err)
	}
	defer resp.Body.Close()

	if reqID := resp.Header.Get("X-Request-ID"); reqID == "" {
		t.Error("X-Request-ID 响应 header 不应为空")
	}
}
