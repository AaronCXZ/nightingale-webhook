package store

import (
	"os"
	"testing"
	"time"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	dbPath := "test_store.db"
	// 清理旧测试数据
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		os.Remove(dbPath)
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
	})
	return s
}

func TestNewAndMigrate(t *testing.T) {
	s := newTestStore(t)
	if s == nil {
		t.Fatal("Store 为 nil")
	}
}

func TestShouldCall(t *testing.T) {
	s := newTestStore(t)

	// 第一次应该允许
	ok, err := s.ShouldCall("hash1", 1*time.Hour)
	if err != nil {
		t.Fatalf("ShouldCall 错误: %v", err)
	}
	if !ok {
		t.Error("第一次 ShouldCall 应该返回 true")
	}

	// 第二次应该拒绝（在冷却期内）
	ok, err = s.ShouldCall("hash1", 1*time.Hour)
	if err != nil {
		t.Fatalf("ShouldCall 错误: %v", err)
	}
	if ok {
		t.Error("第二次 ShouldCall 应该返回 false（冷却期内）")
	}

	// 短冷却期应过期
	ok, err = s.ShouldCall("hash2", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("ShouldCall 错误: %v", err)
	}
	if !ok {
		t.Error("短冷却期第一次 ShouldCall 应该返回 true")
	}
	time.Sleep(10 * time.Millisecond)
	ok, err = s.ShouldCall("hash2", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("ShouldCall 错误: %v", err)
	}
	if !ok {
		t.Error("冷却期过期后 ShouldCall 应该返回 true")
	}
}

func TestGroupCooldown(t *testing.T) {
	s := newTestStore(t)

	first, _ := s.ShouldCallGroup("ops", 1*time.Hour)
	if !first {
		t.Error("第一次 group call 应该允许")
	}
	second, _ := s.ShouldCallGroup("ops", 1*time.Hour)
	if second {
		t.Error("第二次 group call 应该拒绝（冷却期内）")
	}
}

func TestSaveAndGetRecords(t *testing.T) {
	s := newTestStore(t)

	callID := "test-call-001"
	id, err := s.SaveRecord(CallRecord{
		GroupName: "ops",
		AlertName: "CPU过高",
		AlertHash: "abc123",
		Severity:  1,
		Phone:     "13800000000",
		CallID:    &callID,
		CallStatus: CallStatusInitiated,
	})
	if err != nil {
		t.Fatalf("SaveRecord 错误: %v", err)
	}
	if id == 0 {
		t.Error("SaveRecord 应返回非 0 ID")
	}

	records, err := s.GetRecords(10, 0)
	if err != nil {
		t.Fatalf("GetRecords 错误: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("应有 1 条记录，got %d", len(records))
	}
	if records[0].GroupName != "ops" {
		t.Errorf("group_name 应为 ops，got %s", records[0].GroupName)
	}
}

func TestScheduleCRUD(t *testing.T) {
	s := newTestStore(t)
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	// --- Primary CRUD ---

	// Upsert primary
	err := s.UpsertPrimary(OncallPrimary{Date: tomorrow, Name: "张三", Phone: "13800001111"})
	if err != nil {
		t.Fatalf("UpsertPrimary 错误: %v", err)
	}
	primary, err := s.GetOncallPrimary(tomorrow)
	if err != nil {
		t.Fatalf("GetOncallPrimary 错误: %v", err)
	}
	if primary == nil {
		t.Fatal("primary 为 nil")
	}
	if primary.Name != "张三" {
		t.Errorf("Name 应为 张三，got %s", primary.Name)
	}
	if primary.Phone != "13800001111" {
		t.Errorf("Phone 应为 13800001111，got %s", primary.Phone)
	}

	// Update primary
	err = s.UpsertPrimary(OncallPrimary{Date: tomorrow, Name: "李四", Phone: "13900002222"})
	if err != nil {
		t.Fatalf("UpsertPrimary 更新错误: %v", err)
	}
	primary, _ = s.GetOncallPrimary(tomorrow)
	if primary.Name != "李四" {
		t.Errorf("更新后 Name 应为 李四，got %s", primary.Name)
	}

	// Delete primary
	err = s.DeletePrimary(tomorrow)
	if err != nil {
		t.Fatalf("DeletePrimary 错误: %v", err)
	}
	primary, _ = s.GetOncallPrimary(tomorrow)
	if primary != nil {
		t.Error("删除后 primary 应为 nil")
	}

	// --- Backup CRUD ---

	// Upsert backup
	err = s.UpsertBackup(OncallBackup{Date: tomorrow, GroupName: "ops", Name: "王五", Phone: "13700003333"})
	if err != nil {
		t.Fatalf("UpsertBackup 错误: %v", err)
	}
	backups, err := s.GetOncallBackups(tomorrow)
	if err != nil {
		t.Fatalf("GetOncallBackups 错误: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("应有 1 条 backup，got %d", len(backups))
	}
	if backups[0].GroupName != "ops" {
		t.Errorf("GroupName 应为 ops，got %s", backups[0].GroupName)
	}

	// Add second backup
	err = s.UpsertBackup(OncallBackup{Date: tomorrow, GroupName: "web", Name: "赵六", Phone: "13600004444"})
	if err != nil {
		t.Fatalf("UpsertBackup 2 错误: %v", err)
	}
	backups, _ = s.GetOncallBackups(tomorrow)
	if len(backups) != 2 {
		t.Fatalf("应有 2 条 backup，got %d", len(backups))
	}

	// Delete backup
	err = s.DeleteBackup(tomorrow, "ops")
	if err != nil {
		t.Fatalf("DeleteBackup 错误: %v", err)
	}
	backups, _ = s.GetOncallBackups(tomorrow)
	if len(backups) != 1 {
		t.Fatalf("删除后应有 1 条 backup，got %d", len(backups))
	}
	if backups[0].GroupName != "web" {
		t.Errorf("剩余应为 web，got %s", backups[0].GroupName)
	}
}

func TestCalendarMonth(t *testing.T) {
	s := newTestStore(t)

	// 导入整月数据
	primaries := []OncallPrimary{
		{Date: "2026-06-01", Name: "张三", Phone: "13800001111"},
		{Date: "2026-06-02", Name: "李四", Phone: "13900002222"},
	}
	backups := []OncallBackup{
		{Date: "2026-06-01", GroupName: "ops", Name: "王五", Phone: "13700003333"},
		{Date: "2026-06-01", GroupName: "web", Name: "赵六", Phone: "13600004444"},
	}

	pCount, bCount, err := s.ImportSchedule(primaries, backups)
	if err != nil {
		t.Fatalf("ImportSchedule 错误: %v", err)
	}
	if pCount != 2 {
		t.Errorf("应导入 2 条 primary，got %d", pCount)
	}
	if bCount != 2 {
		t.Errorf("应导入 2 条 backup，got %d", bCount)
	}

	days, err := s.GetCalendarMonth("2026-06")
	if err != nil {
		t.Fatalf("GetCalendarMonth 错误: %v", err)
	}
	if len(days) != 30 {
		t.Fatalf("6 月应有 30 天，got %d", len(days))
	}

	// Check first day
	if days[0].PrimaryName != "张三" {
		t.Errorf("6/1 主值应为 张三，got %s", days[0].PrimaryName)
	}
	if len(days[0].Backups) != 2 {
		t.Errorf("6/1 应有 2 个备值，got %d", len(days[0].Backups))
	}

	// Check day without data
	if days[2].PrimaryName != "" {
		t.Errorf("6/3 无主值应为空，got %s", days[2].PrimaryName)
	}
}

func TestStats(t *testing.T) {
	s := newTestStore(t)

	callID := "stats-call-001"
	s.SaveRecord(CallRecord{
		GroupName: "ops", AlertName: "test", AlertHash: "h1",
		Severity: 1, Phone: "13800000001", CallID: &callID,
		CallStatus: CallStatusAnswered, CallDuration: 30,
	})
	callID2 := "stats-call-002"
	s.SaveRecord(CallRecord{
		GroupName: "ops", AlertName: "test2", AlertHash: "h2",
		Severity: 2, Phone: "13800000002", CallID: &callID2,
		CallStatus: CallStatusNoAnswer,
	})

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats 错误: %v", err)
	}
	if stats.TotalCalls != 2 {
		t.Errorf("TotalCalls 应为 2，got %d", stats.TotalCalls)
	}
	if stats.Answered != 1 {
		t.Errorf("Answered 应为 1，got %d", stats.Answered)
	}
	if stats.NoAnswer != 1 {
		t.Errorf("NoAnswer 应为 1，got %d", stats.NoAnswer)
	}
}

func TestCleanupOldRecords(t *testing.T) {
	s := newTestStore(t)

	// 插入一条记录，手动修改时间为 31 天前
	saveCallID := "old-call"
	id, _ := s.SaveRecord(CallRecord{
		GroupName: "ops", AlertName: "old", AlertHash: "oldhash",
		Severity: 1, Phone: "13800000000", CallID: &saveCallID,
	})
	_ = id

	// 清理 30 天以前的记录
	err := s.CleanupOldRecords(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("CleanupOldRecords 错误: %v", err)
	}

	// 确认今天的记录还在
	records, _ := s.GetRecords(10, 0)
	if len(records) != 1 {
		t.Errorf("清理后应有 1 条记录（今天的不应被清理），got %d", len(records))
	}
}
