package schedule

import (
	"strings"
	"testing"
	"time"

	"webhook/internal/store"
)

func TestParseCSV(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	csv := strings.NewReader(`group_name,date,primary_name,primary_phone,backup_name,backup_phone
运维组,` + tomorrow + `,张三,13800001111,李四,13800002222
web组,` + tomorrow + `,王五,13900001111,,`)

	entries, err := ParseCSV(csv)
	if err != nil {
		t.Fatalf("ParseCSV 错误: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应有 2 条记录，got %d", len(entries))
	}

	e1 := entries[0]
	if e1.GroupName != "运维组" {
		t.Errorf("group_name 应为 运维组，got %s", e1.GroupName)
	}
	if e1.PrimaryName != "张三" {
		t.Errorf("PrimaryName 应为 张三，got %s", e1.PrimaryName)
	}
	if e1.PrimaryPhone != "13800001111" {
		t.Errorf("PrimaryPhone 应为 13800001111，got %s", e1.PrimaryPhone)
	}
	if e1.BackupName != "李四" {
		t.Errorf("BackupName 应为 李四，got %s", e1.BackupName)
	}
	if e1.BackupPhone != "13800002222" {
		t.Errorf("BackupPhone 应为 13800002222，got %s", e1.BackupPhone)
	}

	// 第二条无备值班人
	e2 := entries[1]
	if e2.GroupName != "web组" {
		t.Errorf("group_name 应为 web组，got %s", e2.GroupName)
	}
	if e2.BackupName != "" {
		t.Errorf("BackupName 应为空，got %s", e2.BackupName)
	}
}

func TestParseCSVSkipHeader(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	// header 行应被跳过
	csv := strings.NewReader(`group_name,date,primary_name,primary_phone
test,` + tomorrow + `,张三,13800001111`)

	entries, err := ParseCSV(csv)
	if err != nil {
		t.Fatalf("ParseCSV 错误: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应有 1 条记录，got %d", len(entries))
	}
}

func TestParseCSVNoHeader(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	// 无 header 行
	csv := strings.NewReader(`ops,` + tomorrow + `,张三,13800001111`)

	entries, err := ParseCSV(csv)
	if err != nil {
		t.Fatalf("ParseCSV 错误: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应有 1 条记录，got %d", len(entries))
	}
	if entries[0].GroupName != "ops" {
		t.Errorf("GroupName 应为 ops，got %s", entries[0].GroupName)
	}
}

func TestParseCSVPastDate(t *testing.T) {
	// 过去日期应拒绝
	csv := strings.NewReader(`ops,2020-01-01,张三,13800001111`)
	_, err := ParseCSV(csv)
	if err == nil {
		t.Error("过去日期应返回错误")
	}
}

func TestParseCSVInvalidPhone(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	csv := strings.NewReader(`ops,` + tomorrow + `,张三,12345`)
	_, err := ParseCSV(csv)
	if err == nil {
		t.Error("无效手机号应返回错误")
	}
}

func TestParseCSVMissingFields(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	csv := strings.NewReader(`ops,` + tomorrow + `,张三`)
	_, err := ParseCSV(csv)
	if err == nil {
		t.Error("字段不足应返回错误")
	}
}

func TestExportCSV(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	entries := []store.OncallEntry{
		{GroupName: "ops", Date: tomorrow, PrimaryName: "张三", PrimaryPhone: "13800001111", BackupName: "李四", BackupPhone: "13800002222"},
	}

	data, err := ExportCSV(entries)
	if err != nil {
		t.Fatalf("ExportCSV 错误: %v", err)
	}

	csvStr := string(data)
	if !strings.Contains(csvStr, "group_name,date") {
		t.Error("CSV 应包含 header")
	}
	if !strings.Contains(csvStr, "ops") {
		t.Error("CSV 应包含数据行")
	}
	if !strings.Contains(csvStr, "13800001111") {
		t.Error("CSV 应包含主值班人手机号")
	}
}

func TestExportCSVRoundTrip(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")

	original := []store.OncallEntry{
		{GroupName: "ops", Date: tomorrow, PrimaryName: "张三", PrimaryPhone: "13800001111", BackupName: "李四", BackupPhone: "13800002222"},
		{GroupName: "web", Date: tomorrow, PrimaryName: "王五", PrimaryPhone: "13900001111"},
	}

	data, err := ExportCSV(original)
	if err != nil {
		t.Fatalf("ExportCSV 错误: %v", err)
	}

	parsed, err := ParseCSV(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("ParseCSV 错误: %v", err)
	}

	if len(parsed) != len(original) {
		t.Fatalf("round-trip 记录数不匹配: %d vs %d", len(parsed), len(original))
	}

	for i := range original {
		if parsed[i].GroupName != original[i].GroupName {
			t.Errorf("round-trip [%d] group_name 不匹配: %s vs %s", i, parsed[i].GroupName, original[i].GroupName)
		}
		if parsed[i].PrimaryPhone != original[i].PrimaryPhone {
			t.Errorf("round-trip [%d] primary_phone 不匹配", i)
		}
	}
}

func TestValidateDateNotPast(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
	if err := ValidateDateNotPast(tomorrow); err != nil {
		t.Errorf("未来日期不应报错: %v", err)
	}

	if err := ValidateDateNotPast("2020-01-01"); err == nil {
		t.Error("过去日期应报错")
	}
}
