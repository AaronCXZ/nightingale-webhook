package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"webhook/internal/alert"
	"webhook/internal/config"
	"webhook/internal/notifier"
	"webhook/internal/store"
)

const maxBodySize = 1 << 20 // 1MB

// Handler Webhook 核心处理器
type Handler struct {
	store  store.Store
	caller notifier.Caller
	cfg    *config.Config

	// 并发呼叫控制
	mu              sync.Mutex
	concurrentCalls int
}

// New 创建处理器
func New(s store.Store, caller notifier.Caller, cfg *config.Config) *Handler {
	return &Handler{
		store:  s,
		caller: caller,
		cfg:    cfg,
	}
}

// WebhookSummary 返回给夜莺的摘要
type WebhookSummary struct {
	Total   int              `json:"total"`
	Called  int              `json:"called"`
	Skipped int              `json:"skipped"`
	Details []EventResult    `json:"details"`
	DryRun  bool             `json:"dry_run"`
}

// EventResult 单个告警事件处理结果
type EventResult struct {
	Hash      string `json:"hash"`
	RuleName  string `json:"rule_name"`
	Severity  int    `json:"severity"`
	Called    bool   `json:"called"`
	Skipped   bool   `json:"skipped,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Phones    []string `json:"phones,omitempty"`
	CallID    string   `json:"call_id,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// HandleWebhook 处理夜莺告警 webhook
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// 限制请求体大小
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	// 检查 per-request dry_run
	perRequestDryRun := h.cfg.DryRun
	if r.URL.Query().Get("dry_run") == "true" || r.Header.Get("X-Dry-Run") == "true" {
		perRequestDryRun = true
	}

	// 解析 JSON 数组
	var events []alert.Event
	if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体失败: %v", err)})
		return
	}

	if len(events) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "告警事件列表为空"})
		return
	}

	// 处理每个告警
	ctx := r.Context()
	summary := &WebhookSummary{
		Total:   len(events),
		DryRun:  perRequestDryRun,
		Details: make([]EventResult, 0, len(events)),
	}

	for _, event := range events {
		result := h.processEvent(ctx, &event, perRequestDryRun)
		summary.Details = append(summary.Details, result)

		if result.Called {
			summary.Called++
		}
		if result.Skipped {
			summary.Skipped++
		}
	}

	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) processEvent(ctx context.Context, event *alert.Event, dryRun bool) EventResult {
	result := EventResult{
		Hash:     alert.EffectiveHash(event),
		RuleName: event.RuleName,
		Severity: event.Severity,
	}

	// 1. 过滤：跳过已恢复
	if event.IsRecovered {
		slog.Debug("alert skipped: recovered", "rule", event.RuleName, "group", event.GroupName)
		result.Skipped = true
		result.Reason = store.SkippedRecovered
		return result
	}

	// 2. 过滤：跳过低于配置严重等级的告警
	if event.Severity > h.cfg.Alert.MinSeverity {
		slog.Debug("alert skipped: severity too low", "rule", event.RuleName, "severity", event.Severity, "min", h.cfg.Alert.MinSeverity)
		result.Skipped = true
		result.Reason = store.SkippedSeverity
		return result
	}

	effectiveHash := alert.EffectiveHash(event)

	// 3. 告警级去重
	ok, err := h.store.ShouldCall(effectiveHash, h.cfg.Alert.Cooldown)
	if err != nil {
		slog.Error("dedup check failed", "hash", effectiveHash, "error", err)
	}
	if !ok {
		slog.Debug("alert skipped: dedup cooldown", "hash", effectiveHash, "rule", event.RuleName, "group", event.GroupName)
		result.Skipped = true
		result.Reason = store.SkippedDedup
		return result
	}

	// 4. 业务组级限流
	if event.GroupName != "" {
		ok, err := h.store.ShouldCallGroup(event.GroupName, h.cfg.Alert.GroupCooldown)
		if err != nil {
			slog.Error("group cooldown check failed", "group", event.GroupName, "error", err)
		}
		if !ok {
			slog.Debug("alert skipped: group cooldown", "group", event.GroupName, "rule", event.RuleName)
			result.Skipped = true
			result.Reason = store.SkippedGroupCooldown
			return result
		}
	}

	// 5. 拼装电话号码列表
	phones := h.resolvePhones(event)
	if len(phones) == 0 {
		slog.Warn("no phones resolved for alert", "rule", event.RuleName, "group", event.GroupName)
		result.Skipped = true
		result.Reason = store.SkippedNoPhone
		return result
	}
	result.Phones = phones

	// 6. 并发呼叫限制
	if !h.tryIncrement() {
		slog.Warn("concurrent calls at limit, skipping",
			"max", h.cfg.Alert.MaxConcurrentCalls,
			"alert", event.RuleName,
			"group", event.GroupName,
		)
		result.Skipped = true
		result.Reason = store.SkippedConcurrency
		return result
	}
	defer h.decrement()

	// 7. 呼叫
	for _, phone := range phones {
		callResult, err := h.caller.Call(ctx, notifier.CallParams{
			PhoneNumber: phone,
			AlertName:   event.RuleName,
			Severity:    event.Severity,
			Value:       event.TriggerValue,
			GroupName:   event.GroupName,
		})

		var callID *string
		var callStatus string
		var errorMsg *string

		if err != nil {
			callStatus = store.CallStatusFailed
			msg := err.Error()
			errorMsg = &msg
		} else if dryRun {
			callStatus = store.CallStatusInitiated
			cid := "dry-run-" + callResult.CallID
			callID = &cid
		} else {
			callStatus = store.CallStatusInitiated
			callID = &callResult.CallID
		}

		// 写入 next_poll_at（30s 后第一次回查）
		var nextPollAt *string
		if !dryRun && callStatus == store.CallStatusInitiated {
			np := store.NextPollAt(30 * time.Second)
			nextPollAt = &np
		}

		_, saveErr := h.store.SaveRecord(store.CallRecord{
			GroupName:     event.GroupName,
			AlertName:     event.RuleName,
			AlertHash:     effectiveHash,
			Severity:      event.Severity,
			Phone:         phone,
			CallID:        callID,
			CallStatus:    callStatus,
			DryRun:        dryRun,
			ErrorMsg:      errorMsg,
			NextPollAt:    nextPollAt,
		})
		if saveErr != nil {
			slog.Error("保存呼叫记录失败", "error", saveErr)
		}

		if callID != nil {
			result.CallID = *callID
		}
		if errorMsg != nil {
			result.Error = *errorMsg
		}
	}

	result.Called = true
	return result
}

// resolvePhones 拼装电话号码列表
func (h *Handler) resolvePhones(event *alert.Event) []string {
	groups := make(map[string][]string, len(h.cfg.ServerGroups))
	for name, sg := range h.cfg.ServerGroups {
		groups[name] = sg.Phones
	}

	// 新模型：查主值 + 查对应组备值
	var scheduleEntry *alert.OncallEntry
	source := "default"
	if event.GroupName != "" {
		today := time.Now().UTC().Format("2006-01-02")

		primary, err := h.store.GetOncallPrimary(today)
		if err != nil {
			slog.Warn("failed to query primary oncall", "group", event.GroupName, "error", err)
		}
		if primary != nil {
			scheduleEntry = &alert.OncallEntry{
				PrimaryPhone: primary.Phone,
			}
			source = "schedule"

			// 查该组备值
			backups, err := h.store.GetOncallBackups(today)
			if err != nil {
				slog.Warn("failed to query backup oncall", "group", event.GroupName, "error", err)
			}
			for _, b := range backups {
				if b.GroupName == event.GroupName {
					scheduleEntry.BackupPhone = b.Phone
					break
				}
			}
		} else if _, ok := groups[event.GroupName]; ok {
			source = "server_group"
		}
	}

	phones := alert.ResolvePhones(event, scheduleEntry, groups, h.cfg.AlwaysPhones, h.cfg.DefaultPhones)
	slog.Debug("phones resolved",
		"group", event.GroupName,
		"source", source,
		"phones", phones,
		"count", len(phones),
	)
	return phones
}

// tryIncrement 尝试增加并发计数
func (h *Handler) tryIncrement() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.concurrentCalls >= h.cfg.Alert.MaxConcurrentCalls {
		return false
	}
	h.concurrentCalls++
	return true
}

// decrement 减少并发计数
func (h *Handler) decrement() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.concurrentCalls > 0 {
		h.concurrentCalls--
	}
}

// writeJSON 写入 JSON 响应
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("写入 JSON 响应失败", "error", err)
	}
}
