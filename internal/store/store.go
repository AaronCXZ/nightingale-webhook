package store

import (
	"time"
)

// Store 持久化接口
type Store interface {
	// --- 去重与限流 ---

	// ShouldCall 查询 hash 是否在冷却中，若不在冷却中则记录并返回 true
	ShouldCall(hash string, cooldown time.Duration) (bool, error)
	// ShouldCallGroup 查询 group 是否在冷却中，若不在冷却中则记录并返回 true
	ShouldCallGroup(groupName string, cooldown time.Duration) (bool, error)

	// --- 呼叫记录 ---

	// SaveRecord 写入呼叫记录，返回 ID
	SaveRecord(record CallRecord) (int64, error)
	// UpdateCallResult 回查后更新呼叫状态和通话时长
	UpdateCallResult(callID string, status string, duration int64) error
	// UpdateNextPollAt 更新下次回查时间
	UpdateNextPollAt(callID string, nextPollAt string) error
	// GetRecords 分页查询呼叫历史
	GetRecords(limit, offset int) ([]CallRecord, error)
	// GetPendingPolls 获取待回查的记录（next_poll_at <= now）
	GetPendingPolls() ([]CallRecord, error)

	// --- 冷却状态 ---

	// GetCooldowns 获取当前冷却状态列表
	GetCooldowns() ([]CooldownInfo, error)

	// --- 统计 ---

	// GetStats 获取今日聚合统计
	GetStats() (*Stats, error)

	// --- 值班表（新模型：每日一主值 + 多组备值） ---

	// GetOncallPrimary 查某天主值班人
	GetOncallPrimary(date string) (*OncallPrimary, error)
	// GetOncallBackups 查某天所有备值班人
	GetOncallBackups(date string) ([]OncallBackup, error)
	// GetCalendarMonth 获取整月日历数据
	GetCalendarMonth(month string) ([]CalendarDay, error)
	// GetTodaySchedules 获取今日所有值班
	GetTodaySchedules() (*CalendarDay, error)
	// ImportSchedule 批量导入（primary/backup 混合）
	ImportSchedule(primaries []OncallPrimary, backups []OncallBackup) (int, int, error)
	// UpsertPrimary 设置/更新某天主值
	UpsertPrimary(entry OncallPrimary) error
	// UpsertBackup 设置/更新某天某组备值
	UpsertBackup(entry OncallBackup) error
	// DeletePrimary 删除某天主值
	DeletePrimary(date string) error
	// DeleteBackup 删除某天某组备值
	DeleteBackup(date, group string) error
	// LogScheduleChange 记录编辑日志
	LogScheduleChange(change ScheduleChange) error
	// GetScheduleChanges 查询编辑日志
	GetScheduleChanges(limit int) ([]ScheduleChange, error)

	// --- 维护 ---

	// CleanupOldRecords 清理过期呼叫记录
	CleanupOldRecords(retention time.Duration) error
	// Close 关闭数据库连接
	Close() error
}
