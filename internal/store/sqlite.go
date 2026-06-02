package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore 实现 Store 接口
type sqliteStore struct {
	db *sql.DB
}

// New 创建 SQLite Store，自动建表
func New(dbPath string) (Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}

	// 连接池配置
	db.SetMaxOpenConns(1) // SQLite 单写
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &sqliteStore{
		db: db,
	}

	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	return s, nil
}

// migrate 自动建表
func (s *sqliteStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS call_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		group_name TEXT NOT NULL,
		alert_name TEXT NOT NULL,
		alert_hash TEXT NOT NULL,
		severity INTEGER NOT NULL,
		phone TEXT NOT NULL,
		call_id TEXT,
		call_status TEXT DEFAULT 'pending',
		call_duration INTEGER DEFAULT 0,
		retry_count INTEGER DEFAULT 0,
		skipped_reason TEXT,
		dry_run BOOLEAN DEFAULT 0,
		error_msg TEXT,
		next_poll_at TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_call_records_created_at ON call_records(created_at);
	CREATE INDEX IF NOT EXISTS idx_call_records_call_status ON call_records(call_status);
	CREATE INDEX IF NOT EXISTS idx_call_records_next_poll_at ON call_records(next_poll_at);

	CREATE TABLE IF NOT EXISTS oncall_schedule (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_name TEXT NOT NULL,
		date TEXT NOT NULL,
		primary_name TEXT,
		primary_phone TEXT NOT NULL,
		backup_name TEXT,
		backup_phone TEXT,
		UNIQUE(group_name, date)
	);

	CREATE INDEX IF NOT EXISTS idx_oncall_schedule_date ON oncall_schedule(date);
	CREATE INDEX IF NOT EXISTS idx_oncall_schedule_group_date ON oncall_schedule(group_name, date);

	CREATE TABLE IF NOT EXISTS schedule_changes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_name TEXT NOT NULL,
		date TEXT NOT NULL,
		field TEXT NOT NULL,
		old_value TEXT,
		new_value TEXT,
		changed_at TEXT NOT NULL,
		changed_by TEXT DEFAULT 'admin'
	);

	CREATE TABLE IF NOT EXISTS cooldowns (
		key TEXT PRIMARY KEY,
		expires_at TEXT NOT NULL
	);
	`

	_, err := s.db.Exec(schema)
	return err
}

// --- 去重与限流 ---

func (s *sqliteStore) ShouldCall(hash string, cooldown time.Duration) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// 先清理过期冷却
	_, _ = s.db.Exec(`DELETE FROM cooldowns WHERE expires_at <= ?`, now)

	// 检查是否存在且未过期
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM cooldowns WHERE key = ? AND expires_at > ?)`, hash, now).Scan(&exists)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	// 插入
	expires := time.Now().UTC().Add(cooldown).Format(time.RFC3339)
	_, err = s.db.Exec(`INSERT OR REPLACE INTO cooldowns (key, expires_at) VALUES (?, ?)`, hash, expires)
	return err == nil, err
}

func (s *sqliteStore) ShouldCallGroup(groupName string, cooldown time.Duration) (bool, error) {
	return s.ShouldCall("group:"+groupName, cooldown)
}

// --- 呼叫记录 ---

func (s *sqliteStore) SaveRecord(record CallRecord) (int64, error) {
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if record.CallStatus == "" {
		record.CallStatus = CallStatusPending
	}

	result, err := s.db.Exec(`
		INSERT INTO call_records (created_at, group_name, alert_name, alert_hash, severity, phone,
			call_id, call_status, call_duration, retry_count, skipped_reason, dry_run, error_msg, next_poll_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.CreatedAt, record.GroupName, record.AlertName, record.AlertHash, record.Severity,
		record.Phone, record.CallID, record.CallStatus, record.CallDuration, record.RetryCount,
		record.SkippedReason, record.DryRun, record.ErrorMsg, record.NextPollAt,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *sqliteStore) UpdateCallResult(callID string, status string, duration int64) error {
	_, err := s.db.Exec(`
		UPDATE call_records SET call_status = ?, call_duration = ?, next_poll_at = NULL WHERE call_id = ?`,
		status, duration, callID,
	)
	return err
}

func (s *sqliteStore) UpdateNextPollAt(callID string, nextPollAt string) error {
	_, err := s.db.Exec(`UPDATE call_records SET next_poll_at = ? WHERE call_id = ?`, nextPollAt, callID)
	return err
}

func (s *sqliteStore) GetRecords(limit, offset int) ([]CallRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, created_at, group_name, alert_name, alert_hash, severity, phone,
			call_id, call_status, call_duration, retry_count, skipped_reason, dry_run, error_msg, next_poll_at
		FROM call_records ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCallRecords(rows)
}

func (s *sqliteStore) GetPendingPolls() ([]CallRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.Query(`
		SELECT id, created_at, group_name, alert_name, alert_hash, severity, phone,
			call_id, call_status, call_duration, retry_count, skipped_reason, dry_run, error_msg, next_poll_at
		FROM call_records
		WHERE call_status IN (?, ?) AND next_poll_at IS NOT NULL AND next_poll_at <= ?`,
		CallStatusPending, CallStatusInitiated, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCallRecords(rows)
}

func scanCallRecords(rows *sql.Rows) ([]CallRecord, error) {
	var records []CallRecord
	for rows.Next() {
		var r CallRecord
		err := rows.Scan(&r.ID, &r.CreatedAt, &r.GroupName, &r.AlertName, &r.AlertHash,
			&r.Severity, &r.Phone, &r.CallID, &r.CallStatus, &r.CallDuration,
			&r.RetryCount, &r.SkippedReason, &r.DryRun, &r.ErrorMsg, &r.NextPollAt)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// --- 冷却状态 ---

func (s *sqliteStore) GetCooldowns() ([]CooldownInfo, error) {
	now := time.Now().UTC()

	// 先清理过期
	_, _ = s.db.Exec(`DELETE FROM cooldowns WHERE expires_at <= ?`, now.Format(time.RFC3339))

	rows, err := s.db.Query(`SELECT key, expires_at FROM cooldowns WHERE expires_at > ?`, now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var infos []CooldownInfo
	for rows.Next() {
		var info CooldownInfo
		var expiresAt string
		if err := rows.Scan(&info.Key, &expiresAt); err != nil {
			return nil, err
		}
		info.ExpiresAt = expiresAt
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			info.Remaining = t.Sub(now).Truncate(time.Second).String()
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

// --- 统计 ---

func (s *sqliteStore) GetStats() (*Stats, error) {
	today := time.Now().UTC().Format("2006-01-02")
	stats := &Stats{
		ByGroup:    make(map[string]int),
		BySeverity: make(map[int]int),
	}

	// 总计
	row := s.db.QueryRow(`SELECT COUNT(*) FROM call_records WHERE created_at >= ?`, today)
	if err := row.Scan(&stats.TotalCalls); err != nil {
		return nil, err
	}

	// 按状态
	statuses := []struct {
		status string
		target *int
	}{
		{CallStatusAnswered, &stats.Answered},
		{CallStatusNoAnswer, &stats.NoAnswer},
		{CallStatusBusy, &stats.Busy},
		{CallStatusFailed, &stats.Failed},
	}
	for _, st := range statuses {
		row := s.db.QueryRow(`SELECT COUNT(*) FROM call_records WHERE created_at >= ? AND call_status = ?`, today, st.status)
		row.Scan(st.target)
	}

	// 跳过数
	row = s.db.QueryRow(`SELECT COUNT(*) FROM call_records WHERE created_at >= ? AND skipped_reason IS NOT NULL`, today)
	row.Scan(&stats.Skipped)

	// 成功率
	if stats.TotalCalls > 0 {
		called := stats.TotalCalls - stats.Skipped
		if called > 0 {
			stats.SuccessRate = float64(stats.Answered) / float64(called) * 100
		}
	}

	// 按组
	rows, err := s.db.Query(`SELECT group_name, COUNT(*) FROM call_records WHERE created_at >= ? GROUP BY group_name`, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var group string
		var count int
		if err := rows.Scan(&group, &count); err != nil {
			return nil, err
		}
		stats.ByGroup[group] = count
	}

	// 按严重等级
	rows2, err := s.db.Query(`SELECT severity, COUNT(*) FROM call_records WHERE created_at >= ? GROUP BY severity`, today)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var sev, count int
		if err := rows2.Scan(&sev, &count); err != nil {
			return nil, err
		}
		stats.BySeverity[sev] = count
	}

	return stats, nil
}

// --- 值班表 ---

func (s *sqliteStore) GetOncallByDate(groupName, date string) (*OncallEntry, error) {
	var entry OncallEntry
	err := s.db.QueryRow(`
		SELECT id, group_name, date, primary_name, primary_phone, backup_name, backup_phone
		FROM oncall_schedule WHERE group_name = ? AND date = ?`, groupName, date,
	).Scan(&entry.ID, &entry.GroupName, &entry.Date, &entry.PrimaryName,
		&entry.PrimaryPhone, &entry.BackupName, &entry.BackupPhone)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (s *sqliteStore) GetTodaySchedules() ([]OncallEntry, error) {
	today := time.Now().UTC().Format("2006-01-02")
	rows, err := s.db.Query(`
		SELECT id, group_name, date, primary_name, primary_phone, backup_name, backup_phone
		FROM oncall_schedule WHERE date = ? ORDER BY group_name`, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []OncallEntry
	for rows.Next() {
		var e OncallEntry
		if err := rows.Scan(&e.ID, &e.GroupName, &e.Date, &e.PrimaryName,
			&e.PrimaryPhone, &e.BackupName, &e.BackupPhone); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *sqliteStore) ImportSchedule(entries []OncallEntry) (int, error) {
	count := 0
	for _, e := range entries {
		result, err := s.db.Exec(`
			INSERT INTO oncall_schedule (group_name, date, primary_name, primary_phone, backup_name, backup_phone)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(group_name, date) DO UPDATE SET
				primary_name = excluded.primary_name,
				primary_phone = excluded.primary_phone,
				backup_name = excluded.backup_name,
				backup_phone = excluded.backup_phone`,
			e.GroupName, e.Date, e.PrimaryName, e.PrimaryPhone, e.BackupName, e.BackupPhone,
		)
		if err != nil {
			return count, err
		}
		n, _ := result.RowsAffected()
		if n > 0 {
			count++
		}
	}
	return count, nil
}

func (s *sqliteStore) UpdateScheduleEntry(groupName, date string, entry OncallEntry) error {
	_, err := s.db.Exec(`
		UPDATE oncall_schedule SET primary_name=?, primary_phone=?, backup_name=?, backup_phone=?
		WHERE group_name=? AND date=?`,
		entry.PrimaryName, entry.PrimaryPhone, entry.BackupName, entry.BackupPhone,
		groupName, date,
	)
	return err
}

func (s *sqliteStore) DeleteScheduleEntry(groupName, date string) error {
	_, err := s.db.Exec(`DELETE FROM oncall_schedule WHERE group_name=? AND date=?`, groupName, date)
	return err
}

func (s *sqliteStore) ExportSchedule(startDate, endDate string) ([]OncallEntry, error) {
	query, dates := `SELECT id, group_name, date, primary_name, primary_phone, backup_name, backup_phone FROM oncall_schedule`, []interface{}{}

	if startDate != "" && endDate != "" {
		query += ` WHERE date >= ? AND date <= ?`
		dates = append(dates, startDate, endDate)
	} else if startDate != "" {
		query += ` WHERE date >= ?`
		dates = append(dates, startDate)
	}
	query += ` ORDER BY date, group_name`

	rows, err := s.db.Query(query, dates...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []OncallEntry
	for rows.Next() {
		var e OncallEntry
		if err := rows.Scan(&e.ID, &e.GroupName, &e.Date, &e.PrimaryName,
			&e.PrimaryPhone, &e.BackupName, &e.BackupPhone); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *sqliteStore) LogScheduleChange(change ScheduleChange) error {
	if change.ChangedAt == "" {
		change.ChangedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if change.ChangedBy == "" {
		change.ChangedBy = "admin"
	}
	_, err := s.db.Exec(`
		INSERT INTO schedule_changes (group_name, date, field, old_value, new_value, changed_at, changed_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		change.GroupName, change.Date, change.Field, change.OldValue, change.NewValue,
		change.ChangedAt, change.ChangedBy,
	)
	return err
}

func (s *sqliteStore) GetScheduleChanges(limit int) ([]ScheduleChange, error) {
	rows, err := s.db.Query(`
		SELECT id, group_name, date, field, old_value, new_value, changed_at, changed_by
		FROM schedule_changes ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []ScheduleChange
	for rows.Next() {
		var c ScheduleChange
		if err := rows.Scan(&c.ID, &c.GroupName, &c.Date, &c.Field, &c.OldValue,
			&c.NewValue, &c.ChangedAt, &c.ChangedBy); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	return changes, rows.Err()
}

// --- 维护 ---

func (s *sqliteStore) CleanupOldRecords(retention time.Duration) error {
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339)
	_, err := s.db.Exec(`DELETE FROM call_records WHERE created_at < ?`, cutoff)
	return err
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

// --- 确保实现接口 ---
var _ Store = (*sqliteStore)(nil)
