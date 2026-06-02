package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"webhook/internal/config"
	"webhook/internal/notifier"
	"webhook/internal/store"
)

func TestHandleWebhookDryRun(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	defer s.Close()

	mockCaller := &notifier.MockCaller{}
	cfg := &config.Config{
		Alert: config.AlertConfig{
			MinSeverity:        3,
			Cooldown:           15 * time.Minute,
			GroupCooldown:      5 * time.Minute,
			MaxConcurrentCalls: 5,
		},
		DryRun:        true,
		AlwaysPhones:  []string{"13600000000"},
	}

	h := New(s, mockCaller, cfg)

	// 构造测试告警
	events := []map[string]interface{}{
		{
			"id":               1,
			"rule_name":       "CPU过高",
			"severity":        2,
			"is_recovered":    false,
			"trigger_value":   "95.5",
			"hash":            "test-hash-001",
			"group_name":      "ops",
			"notify_users_obj": []map[string]string{{"phone": "13800000000", "username": "oncall"}},
			"tags_map":        map[string]string{},
			"annotations":     map[string]string{},
		},
	}

	body, _ := json.Marshal(events)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nightingale/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleWebhook(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("状态码应为 200，got %d", rr.Code)
	}

	var summary WebhookSummary
	if err := json.NewDecoder(rr.Body).Decode(&summary); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if summary.Total != 1 {
		t.Errorf("Total 应为 1，got %d", summary.Total)
	}
	if !summary.DryRun {
		t.Error("DryRun 应为 true")
	}
	if summary.Details[0].Called != true {
		t.Error("dry run 模式下仍应标记为 called")
	}
	if summary.Details[0].Phones == nil || len(summary.Details[0].Phones) == 0 {
		t.Error("应有电话号码")
	}
}

func TestHandleWebhookRecoveredFilter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	defer s.Close()

	h := New(s, &notifier.MockCaller{}, &config.Config{
		Alert: config.AlertConfig{
			MinSeverity:        3,
			Cooldown:           15 * time.Minute,
			GroupCooldown:      5 * time.Minute,
			MaxConcurrentCalls: 5,
		},
		DryRun: true,
	})

	events := []map[string]interface{}{
		{
			"id":             2,
			"rule_name":      "已恢复告警",
			"severity":       1,
			"is_recovered":   true,
			"trigger_value":  "0",
			"hash":           "test-hash-002",
			"group_name":     "ops",
			"tags_map":       map[string]string{},
			"annotations":    map[string]string{},
		},
	}

	body, _ := json.Marshal(events)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nightingale/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleWebhook(rr, req)

	var summary WebhookSummary
	json.NewDecoder(rr.Body).Decode(&summary)

	if summary.Details[0].Skipped != true {
		t.Error("已恢复告警应被跳过")
	}
	if summary.Details[0].Reason != store.SkippedRecovered {
		t.Errorf("跳过原因应为 %s，got %s", store.SkippedRecovered, summary.Details[0].Reason)
	}
}

func TestHandleWebhookSeverityFilter(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	defer s.Close()

	h := New(s, &notifier.MockCaller{}, &config.Config{
		Alert: config.AlertConfig{
			MinSeverity:        2, // 只处理严重(1)和警告(2)
			Cooldown:           15 * time.Minute,
			GroupCooldown:      5 * time.Minute,
			MaxConcurrentCalls: 5,
		},
		DryRun: true,
	})

	events := []map[string]interface{}{
		{
			"id":             3,
			"rule_name":      "提醒级告警",
			"severity":       3, // 3=提醒，高于 min 2, 会被跳过
			"is_recovered":   false,
			"trigger_value":  "80",
			"hash":           "test-hash-003",
			"group_name":     "ops",
			"tags_map":       map[string]string{},
			"annotations":    map[string]string{},
		},
	}

	body, _ := json.Marshal(events)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nightingale/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleWebhook(rr, req)

	var summary WebhookSummary
	json.NewDecoder(rr.Body).Decode(&summary)

	if summary.Details[0].Skipped != true {
		t.Error("低于严重等级的告警应被跳过")
	}
	if summary.Details[0].Reason != store.SkippedSeverity {
		t.Errorf("跳过原因应为 %s，got %s", store.SkippedSeverity, summary.Details[0].Reason)
	}
}

func TestHandleWebhookDedup(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	defer s.Close()

	h := New(s, &notifier.MockCaller{}, &config.Config{
		Alert: config.AlertConfig{
			MinSeverity:        3,
			Cooldown:           1 * time.Hour,
			GroupCooldown:      5 * time.Minute,
			MaxConcurrentCalls: 5,
		},
		DryRun:        true,
		AlwaysPhones:  []string{"13600000000"},
	})

	event := map[string]interface{}{
		"id":             4,
		"rule_name":      "去重测试",
		"severity":       1,
		"is_recovered":   false,
		"trigger_value":  "90",
		"hash":           "test-hash-dedup",
		"group_name":     "ops",
		"tags_map":       map[string]string{},
		"annotations":    map[string]string{},
	}

	body, _ := json.Marshal([]map[string]interface{}{event})

	// 第一次请求
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/nightingale/webhook", bytes.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	h.HandleWebhook(rr1, req1)

	var s1 WebhookSummary
	json.NewDecoder(rr1.Body).Decode(&s1)
	if !s1.Details[0].Called {
		t.Error("第一次应成功呼叫")
	}

	// 第二次请求（相同 hash，应被去重）
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/nightingale/webhook", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	h.HandleWebhook(rr2, req2)

	var s2 WebhookSummary
	json.NewDecoder(rr2.Body).Decode(&s2)
	if !s2.Details[0].Skipped {
		t.Error("第二次应被去重跳过")
	}
	if s2.Details[0].Reason != store.SkippedDedup {
		t.Errorf("跳过原因应为 %s，got %s", store.SkippedDedup, s2.Details[0].Reason)
	}
}

func TestHandleWebhookEmptyBody(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	defer s.Close()

	h := New(s, &notifier.MockCaller{}, &config.Config{
		Alert: config.AlertConfig{MinSeverity: 3, Cooldown: 15 * time.Minute, GroupCooldown: 5 * time.Minute, MaxConcurrentCalls: 5},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/nightingale/webhook", bytes.NewReader([]byte("[]")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleWebhook(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("空数组应返回 400，got %d", rr.Code)
	}
}

func TestPerRequestDryRun(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	defer s.Close()

	// 全局 dry_run = false，但通过查询参数开启
	h := New(s, &notifier.MockCaller{}, &config.Config{
		Alert: config.AlertConfig{
			MinSeverity:        3,
			Cooldown:           15 * time.Minute,
			GroupCooldown:      5 * time.Minute,
			MaxConcurrentCalls: 5,
		},
		DryRun:        false,
		AlwaysPhones:  []string{"13600000000"},
	})

	event := map[string]interface{}{
		"id": 5, "rule_name": "per-request dry",
		"severity": 1, "is_recovered": false, "trigger_value": "90",
		"hash": "test-hash-prdr", "group_name": "ops",
		"tags_map": map[string]string{}, "annotations": map[string]string{},
	}

	body, _ := json.Marshal([]map[string]interface{}{event})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nightingale/webhook?dry_run=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleWebhook(rr, req)

	var summary WebhookSummary
	json.NewDecoder(rr.Body).Decode(&summary)

	if !summary.DryRun {
		t.Error("per-request dry_run 应为 true")
	}
}

// 确保 Handler 不持有 context
var _ context.Context // unused
