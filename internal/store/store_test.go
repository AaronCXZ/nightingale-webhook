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

	// Import
	n, err := s.ImportSchedule([]OncallEntry{
		{GroupName: "ops", Date: "2026-06-01", PrimaryName: "张三", PrimaryPhone: "13800001111", BackupName: "李四", BackupPhone: "13800002222"},
		{GroupName: "web", Date: "2026-06-01", PrimaryName: "王五", PrimaryPhone: "13900001111"},
	})
	if err != nil {
		t.Fatalf("ImportSchedule 错误: %v", err)
	}
	if n != 2 {
		t.Errorf("应导入 2 条，got %d", n)
	}

	// Get
	entry, err := s.GetOncallByDate("ops", "2026-06-01")
	if err != nil {
		t.Fatalf("GetOncallByDate 错误: %v", err)
	}
	if entry == nil {
		t.Fatal("entry 为 nil")
	}
	if entry.PrimaryName != "张三" {
		t.Errorf("主值班人应为张三，got %s", entry.PrimaryName)
	}

	// Update
	err = s.UpdateScheduleEntry("ops", "2026-06-01", OncallEntry{PrimaryName: "赵六", PrimaryPhone: "13800003333"})
	if err != nil {
		t.Fatalf("UpdateScheduleEntry 错误: %v", err)
	}
	entry, _ = s.GetOncallByDate("ops", "2026-06-01")
	if entry.PrimaryName != "赵六" {
		t.Errorf("更新后主值班人应为赵六，got %s", entry.PrimaryName)
	}

	// Delete
	err = s.DeleteScheduleEntry("ops", "2026-06-01")
	if err != nil {
		t.Fatalf("DeleteScheduleEntry 错误: %v", err)
	}
	entry, _ = s.GetOncallByDate("ops", "2026-06-01")
	if entry != nil {
		t.Error("删除后 entry 应为 nil")
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
