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

	// --- 值班表 ---

	// GetOncallByDate 按日期和组名查值班表
	GetOncallByDate(groupName, date string) (*OncallEntry, error)
	// GetTodaySchedules 获取今日所有组的值班表
	GetTodaySchedules() ([]OncallEntry, error)
	// ImportSchedule 批量导入值班表（UPSERT by group_name, date）
	ImportSchedule(entries []OncallEntry) (int, error)
	// UpdateScheduleEntry 更新单条排班
	UpdateScheduleEntry(groupName, date string, entry OncallEntry) error
	// DeleteScheduleEntry 删除某天排班
	DeleteScheduleEntry(groupName, date string) error
	// ExportSchedule 导出日期范围值班表
	ExportSchedule(startDate, endDate string) ([]OncallEntry, error)
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
