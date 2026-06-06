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

// migrate 自动建表（含旧模型迁移）
func (s *sqliteStore) migrate() error {
	_, _ = s.db.Exec(`DROP TABLE IF EXISTS oncall_schedule`)
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

	CREATE TABLE IF NOT EXISTS oncall_primary (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		phone TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_oncall_primary_date ON oncall_primary(date);

	CREATE TABLE IF NOT EXISTS oncall_backup (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		group_name TEXT NOT NULL,
		name TEXT,
		phone TEXT NOT NULL,
		UNIQUE(date, group_name)
	);
	CREATE INDEX IF NOT EXISTS idx_oncall_backup_date ON oncall_backup(date);

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
	);`

	_, err := s.db.Exec(schema)
	return err
}

// --- 去重与限流 ---

func (s *sqliteStore) ShouldCall(hash string, cooldown time.Duration) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.Exec(`DELETE FROM cooldowns WHERE expires_at <= ?`, now)

	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM cooldowns WHERE key = ? AND expires_at > ?)`, hash, now).Scan(&exists)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

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

	row := s.db.QueryRow(`SELECT COUNT(*) FROM call_records WHERE created_at >= ?`, today)
	if err := row.Scan(&stats.TotalCalls); err != nil {
		return nil, err
	}

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

	row = s.db.QueryRow(`SELECT COUNT(*) FROM call_records WHERE created_at >= ? AND skipped_reason IS NOT NULL`, today)
	row.Scan(&stats.Skipped)

	if stats.TotalCalls > 0 {
		called := stats.TotalCalls - stats.Skipped
		if called > 0 {
			stats.SuccessRate = float64(stats.Answered) / float64(called) * 100
		}
	}

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

// --- 值班表（新模型） ---

func (s *sqliteStore) GetOncallPrimary(date string) (*OncallPrimary, error) {
	var entry OncallPrimary
	err := s.db.QueryRow(
		`SELECT id, date, name, phone FROM oncall_primary WHERE date = ?`, date,
	).Scan(&entry.ID, &entry.Date, &entry.Name, &entry.Phone)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (s *sqliteStore) GetOncallBackups(date string) ([]OncallBackup, error) {
	rows, err := s.db.Query(
		`SELECT id, date, group_name, name, phone FROM oncall_backup WHERE date = ? ORDER BY group_name`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []OncallBackup
	for rows.Next() {
		var e OncallBackup
		if err := rows.Scan(&e.ID, &e.Date, &e.GroupName, &e.Name, &e.Phone); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *sqliteStore) GetCalendarMonth(month string) ([]CalendarDay, error) {
	startDate := month + "-01"
	t, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("无效月份: %s", month)
	}
	endDate := t.AddDate(0, 1, -1).Format("2006-01-02")

	// 获取当月所有主值
	primeRows, err := s.db.Query(
		`SELECT date, name, phone FROM oncall_primary WHERE date >= ? AND date <= ? ORDER BY date`,
		startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer primeRows.Close()

	primaryMap := make(map[string]struct{ name, phone string })
	for primeRows.Next() {
		var date, name, phone string
		if err := primeRows.Scan(&date, &name, &phone); err != nil {
			return nil, err
		}
		primaryMap[date] = struct{ name, phone string }{name, phone}
	}

	// 获取当月所有备值
	backupRows, err := s.db.Query(
		`SELECT date, group_name, name, phone FROM oncall_backup WHERE date >= ? AND date <= ? ORDER BY date, group_name`,
		startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer backupRows.Close()

	backupMap := make(map[string][]OncallBackup)
	for backupRows.Next() {
		var e OncallBackup
		if err := backupRows.Scan(&e.Date, &e.GroupName, &e.Name, &e.Phone); err != nil {
			return nil, err
		}
		backupMap[e.Date] = append(backupMap[e.Date], e)
	}

	// 遍历整月
	var days []CalendarDay
	current := t
	for !current.After(t.AddDate(0, 1, -1)) {
		ds := current.Format("2006-01-02")
		day := CalendarDay{Date: ds}
		if p, ok := primaryMap[ds]; ok {
			day.PrimaryName = p.name
			day.PrimaryPhone = p.phone
		}
		if b, ok := backupMap[ds]; ok {
			day.Backups = b
		}
		days = append(days, day)
		current = current.AddDate(0, 0, 1)
	}
	return days, nil
}

func (s *sqliteStore) GetTodaySchedules() (*CalendarDay, error) {
	today := time.Now().UTC().Format("2006-01-02")

	primary, err := s.GetOncallPrimary(today)
	if err != nil {
		return nil, err
	}

	backups, err := s.GetOncallBackups(today)
	if err != nil {
		return nil, err
	}

	day := &CalendarDay{Date: today}
	if primary != nil {
		day.PrimaryName = primary.Name
		day.PrimaryPhone = primary.Phone
	}
	day.Backups = backups
	return day, nil
}

func (s *sqliteStore) ImportSchedule(primaries []OncallPrimary, backups []OncallBackup) (int, int, error) {
	pCount := 0
	for _, e := range primaries {
		result, err := s.db.Exec(`
			INSERT INTO oncall_primary (date, name, phone)
			VALUES (?, ?, ?)
			ON CONFLICT(date) DO UPDATE SET name=excluded.name, phone=excluded.phone`,
			e.Date, e.Name, e.Phone)
		if err != nil {
			return pCount, 0, err
		}
		n, _ := result.RowsAffected()
		if n > 0 {
			pCount++
		}
	}

	bCount := 0
	for _, e := range backups {
		result, err := s.db.Exec(`
			INSERT INTO oncall_backup (date, group_name, name, phone)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(date, group_name) DO UPDATE SET name=excluded.name, phone=excluded.phone`,
			e.Date, e.GroupName, e.Name, e.Phone)
		if err != nil {
			return pCount, bCount, err
		}
		n, _ := result.RowsAffected()
		if n > 0 {
			bCount++
		}
	}
	return pCount, bCount, nil
}

func (s *sqliteStore) UpsertPrimary(entry OncallPrimary) error {
	_, err := s.db.Exec(`
		INSERT INTO oncall_primary (date, name, phone)
		VALUES (?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET name=excluded.name, phone=excluded.phone`,
		entry.Date, entry.Name, entry.Phone)
	return err
}

func (s *sqliteStore) UpsertBackup(entry OncallBackup) error {
	_, err := s.db.Exec(`
		INSERT INTO oncall_backup (date, group_name, name, phone)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(date, group_name) DO UPDATE SET name=excluded.name, phone=excluded.phone`,
		entry.Date, entry.GroupName, entry.Name, entry.Phone)
	return err
}

func (s *sqliteStore) DeletePrimary(date string) error {
	_, err := s.db.Exec(`DELETE FROM oncall_primary WHERE date = ?`, date)
	return err
}

func (s *sqliteStore) DeleteBackup(date, group string) error {
	_, err := s.db.Exec(`DELETE FROM oncall_backup WHERE date = ? AND group_name = ?`, date, group)
	return err
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
