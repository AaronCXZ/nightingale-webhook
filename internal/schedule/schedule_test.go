package schedule

import (
	"strings"
	"testing"
	"time"

	"webhook/internal/store"
)

func tomorrow(t *testing.T) string {
	t.Helper()
	return time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
}

func TestParseCSV(t *testing.T) {
	day := tomorrow(t)

	csv := strings.NewReader(`type,date,group_name,name,phone
primary,` + day + `,main,张三,13800001111
backup,` + day + `,ops,李四,13800002222
backup,` + day + `,web,王五,13900001111`)

	primaries, backups, err := ParseCSV(csv)
	if err != nil {
		t.Fatalf("ParseCSV 错误: %v", err)
	}

	if len(primaries) != 1 {
		t.Fatalf("应有 1 条 primary，got %d", len(primaries))
	}
	if primaries[0].Name != "张三" {
		t.Errorf("primary name 应为 张三，got %s", primaries[0].Name)
	}
	if primaries[0].Phone != "13800001111" {
		t.Errorf("primary phone 应为 13800001111，got %s", primaries[0].Phone)
	}

	if len(backups) != 2 {
		t.Fatalf("应有 2 条 backup，got %d", len(backups))
	}
	if backups[0].GroupName != "ops" {
		t.Errorf("backup[0] group_name 应为 ops，got %s", backups[0].GroupName)
	}
	if backups[1].GroupName != "web" {
		t.Errorf("backup[1] group_name 应为 web，got %s", backups[1].GroupName)
	}
}

func TestParseCSVNoHeader(t *testing.T) {
	day := tomorrow(t)
	csv := strings.NewReader(`primary,` + day + `,main,张三,13800001111`)

	primaries, backups, err := ParseCSV(csv)
	if err != nil {
		t.Fatalf("ParseCSV 错误: %v", err)
	}
	if len(primaries) != 1 {
		t.Fatalf("应有 1 条 primary，got %d", len(primaries))
	}
	if len(backups) != 0 {
		t.Errorf("应有 0 条 backup，got %d", len(backups))
	}
}

func TestParseCSVPastDate(t *testing.T) {
	csv := strings.NewReader(`primary,2020-01-01,main,张三,13800001111`)
	_, _, err := ParseCSV(csv)
	if err == nil {
		t.Error("过去日期应返回错误")
	}
}

func TestParseCSVInvalidPhone(t *testing.T) {
	day := tomorrow(t)
	csv := strings.NewReader(`primary,` + day + `,main,张三,12345`)
	_, _, err := ParseCSV(csv)
	if err == nil {
		t.Error("无效手机号应返回错误")
	}
}

func TestParseCSVMissingFields(t *testing.T) {
	day := tomorrow(t)
	csv := strings.NewReader(`primary,` + day + `,main`)
	_, _, err := ParseCSV(csv)
	if err == nil {
		t.Error("字段不足应返回错误")
	}
}

func TestParseCSVInvalidType(t *testing.T) {
	day := tomorrow(t)
	csv := strings.NewReader(`invalid,` + day + `,main,张三,13800001111`)
	_, _, err := ParseCSV(csv)
	if err == nil {
		t.Error("无效类型应返回错误")
	}
}

func TestParseCSVBackupNoGroup(t *testing.T) {
	day := tomorrow(t)
	csv := strings.NewReader(`backup,` + day + `,,张三,13800001111`)
	_, _, err := ParseCSV(csv)
	if err == nil {
		t.Error("backup 类型缺少 group_name 应返回错误")
	}
}

func TestExportCSV(t *testing.T) {
	day := tomorrow(t)
	days := []store.CalendarDay{
		{
			Date:         day,
			PrimaryName:  "张三",
			PrimaryPhone: "13800001111",
			Backups: []store.OncallBackup{
				{Date: day, GroupName: "ops", Name: "李四", Phone: "13800002222"},
			},
		},
	}

	data, err := ExportCSV(days)
	if err != nil {
		t.Fatalf("ExportCSV 错误: %v", err)
	}

	csvStr := string(data)
	if !strings.Contains(csvStr, "type,date,group_name") {
		t.Error("CSV 应包含 header")
	}
	if !strings.Contains(csvStr, "primary") {
		t.Error("CSV 应包含 primary 行")
	}
	if !strings.Contains(csvStr, "backup") {
		t.Error("CSV 应包含 backup 行")
	}
	if !strings.Contains(csvStr, "13800001111") {
		t.Error("CSV 应包含电话号码")
	}
}

func TestExportCSVRoundTrip(t *testing.T) {
	day := tomorrow(t)
	days := []store.CalendarDay{
		{
			Date:         day,
			PrimaryName:  "张三",
			PrimaryPhone: "13800001111",
			Backups: []store.OncallBackup{
				{Date: day, GroupName: "ops", Name: "李四", Phone: "13800002222"},
			},
		},
		{
			Date:         time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02"),
			PrimaryName:  "王五",
			PrimaryPhone: "13900001111",
		},
	}

	data, err := ExportCSV(days)
	if err != nil {
		t.Fatalf("ExportCSV 错误: %v", err)
	}

	primaries, backups, err := ParseCSV(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("ParseCSV round-trip 错误: %v", err)
	}

	if len(primaries) != 2 {
		t.Fatalf("round-trip primary 应为 2，got %d", len(primaries))
	}
	if len(backups) != 1 {
		t.Errorf("round-trip backup 应为 1，got %d", len(backups))
	}
}

func TestValidateDateNotPast(t *testing.T) {
	day := tomorrow(t)
	if err := ValidateDateNotPast(day); err != nil {
		t.Errorf("未来日期不应报错: %v", err)
	}
	if err := ValidateDateNotPast("2020-01-01"); err == nil {
		t.Error("过去日期应报错")
	}
}
