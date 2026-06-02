package alert

import (
	"testing"
)

func TestSeverityLabel(t *testing.T) {
	tests := []struct {
		level int
		want  string
	}{
		{1, "严重"},
		{2, "警告"},
		{3, "提醒"},
		{0, "未知"},
		{99, "未知"},
	}
	for _, tt := range tests {
		got := SeverityLabel(tt.level)
		if got != tt.want {
			t.Errorf("SeverityLabel(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestEffectiveHash(t *testing.T) {
	event := &Event{
		Hash:      "abc123",
		RuleName:  "CPU过高",
		GroupName: "ops",
	}
	if got := EffectiveHash(event); got != "abc123" {
		t.Errorf("EffectiveHash with hash = %q, want %q", got, "abc123")
	}

	event.Hash = ""
	fallback := EffectiveHash(event)
	if fallback == "" {
		t.Error("EffectiveHash without hash should return fallback, got empty")
	}

	// 确定性：相同输入应有相同输出
	event2 := &Event{RuleName: "CPU过高", GroupName: "ops"}
	if EffectiveHash(event) != EffectiveHash(event2) {
		t.Error("EffectiveHash should be deterministic for same inputs")
	}

	// 不同输入应不同
	event3 := &Event{RuleName: "内存不足", GroupName: "ops"}
	if EffectiveHash(event) == EffectiveHash(event3) {
		t.Error("EffectiveHash should differ for different inputs")
	}
}

func TestResolvePhones(t *testing.T) {
	groups := map[string][]string{
		"web":   {"13800001111", "13800002222"},
		"db":    {"13900003333"},
	}
	always := []string{"13600000000"}
	defaults := []string{"13700000000"}

	// 1. 值班表匹配
	event := &Event{GroupName: "web"}
	schedule := &OncallEntry{PrimaryPhone: "13500000000", BackupPhone: "13511111111"}
	phones := ResolvePhones(event, schedule, groups, always, defaults)
	if len(phones) != 3 { // primary + backup + always
		t.Errorf("值班表匹配应得 3 个号码，got %d: %v", len(phones), phones)
	}

	// 2. server_groups 匹配
	event2 := &Event{GroupName: "db"}
	phones = ResolvePhones(event2, nil, groups, always, defaults)
	if len(phones) != 2 { // db group 1 phone + always
		t.Errorf("server_groups 匹配应得 2 个号码，got %d: %v", len(phones), phones)
	}

	// 3. default_phones 兜底
	event3 := &Event{GroupName: "unknown"}
	phones = ResolvePhones(event3, nil, groups, always, defaults)
	if len(phones) != 2 { // default + always
		t.Errorf("default_phones 兜底应得 2 个号码，got %d: %v", len(phones), phones)
	}

	// 4. 去重：always_phones 和 server_groups 重复时去重
	groups2 := map[string][]string{"ops": {"13600000000"}} // 和 always 重复
	phones = ResolvePhones(&Event{GroupName: "ops"}, nil, groups2, always, defaults)
	if len(phones) != 1 { // ops=136 + always=136 去重 → 1
		t.Errorf("去重失败，got %d: %v", len(phones), phones)
	}
}

func TestResolvePhonesEmptyScheduleBackup(t *testing.T) {
	// 值班表只有主值班人，无备值班人
	schedule := &OncallEntry{PrimaryPhone: "13500000000"}

	phones := ResolvePhones(
		&Event{GroupName: "web"},
		schedule,
		map[string][]string{"web": {"13800001111"}},
		[]string{},
		[]string{},
	)
	if len(phones) != 1 {
		t.Errorf("只有主值班人应得 1 个号码，got %d", len(phones))
	}
	if phones[0] != "13500000000" {
		t.Errorf("主值班人号码应为 13500000000，got %s", phones[0])
	}
}
