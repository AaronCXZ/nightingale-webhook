package store

import "time"

// CallRecord 一条呼叫记录
type CallRecord struct {
	ID             int64   `json:"id"`
	CreatedAt      string  `json:"created_at"`
	GroupName      string  `json:"group_name"`
	AlertName      string  `json:"alert_name"`
	AlertHash      string  `json:"alert_hash"`
	Severity       int     `json:"severity"`
	Phone          string  `json:"phone"`
	CallID         *string `json:"call_id"`
	CallStatus     string  `json:"call_status"` // pending/initiated/answered/no_answer/busy/failed
	CallDuration   int64   `json:"call_duration"`
	RetryCount     int     `json:"retry_count"`
	SkippedReason  *string `json:"skipped_reason"`
	DryRun         bool    `json:"dry_run"`
	ErrorMsg       *string `json:"error_msg"`
	NextPollAt     *string `json:"next_poll_at"`
}

// OncallEntry 值班表条目
type OncallEntry struct {
	ID           int64  `json:"id"`
	GroupName    string `json:"group_name" csv:"group_name"`
	Date         string `json:"date" csv:"date"` // YYYY-MM-DD
	PrimaryName  string `json:"primary_name" csv:"primary_name"`
	PrimaryPhone string `json:"primary_phone" csv:"primary_phone"`
	BackupName   string `json:"backup_name" csv:"backup_name"`
	BackupPhone  string `json:"backup_phone" csv:"backup_phone"`
}

// ScheduleChange 值班表编辑日志
type ScheduleChange struct {
	ID        int64  `json:"id"`
	GroupName string `json:"group_name"`
	Date      string `json:"date"`
	Field     string `json:"field"`
	OldValue  string `json:"old_value"`
	NewValue  string `json:"new_value"`
	ChangedAt string `json:"changed_at"`
	ChangedBy string `json:"changed_by"`
}

// CooldownInfo 冷却状态
type CooldownInfo struct {
	Key       string `json:"key"`
	ExpiresAt string `json:"expires_at"`
	Remaining string `json:"remaining"` // 剩余冷却时间
}

// Stats 聚合统计
type Stats struct {
	TotalCalls    int                `json:"total_calls"`
	Answered      int                `json:"answered"`
	NoAnswer      int                `json:"no_answer"`
	Busy          int                `json:"busy"`
	Failed        int                `json:"failed"`
	Skipped       int                `json:"skipped"`
	ByGroup       map[string]int     `json:"by_group"`
	BySeverity    map[int]int        `json:"by_severity"`
	SuccessRate  float64             `json:"success_rate"`
}

// CallStatus 常量
const (
	CallStatusPending    = "pending"
	CallStatusInitiated  = "initiated"
	CallStatusAnswered   = "answered"
	CallStatusNoAnswer   = "no_answer"
	CallStatusBusy       = "busy"
	CallStatusFailed     = "failed"
)

// SkippedReason 常量
const (
	SkippedRecovered      = "recovered"
	SkippedSeverity       = "severity"
	SkippedDedup          = "dedup"
	SkippedGroupCooldown  = "group_cooldown"
	SkippedNoPhone        = "nophone"
	SkippedConcurrency    = "concurrency"
)

// NextPollAt helper
func NextPollAt(after time.Duration) string {
	return time.Now().UTC().Add(after).Format(time.RFC3339)
}
