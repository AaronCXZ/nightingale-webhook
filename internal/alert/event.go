package alert

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// Event 夜莺告警事件
type Event struct {
	ID             int64             `json:"id"`
	RuleName       string            `json:"rule_name"`
	Severity       int               `json:"severity"`
	IsRecovered    bool              `json:"is_recovered"`
	TriggerValue   string            `json:"trigger_value"`
	NotifyUsersObj []NotifyUser      `json:"notify_users_obj"`
	Hash           string            `json:"hash"`
	GroupName      string            `json:"group_name"`
	TagsMap        map[string]string `json:"tags_map"`
	Annotations    map[string]string `json:"annotations"`
}

// NotifyUser 通知用户
type NotifyUser struct {
	Phone    string `json:"phone"`
	Username string `json:"username"`
}

// OncallEntry 值班表条目（用于 ResolvePhones）
type OncallEntry struct {
	PrimaryPhone string
	BackupPhone  string
}

// ResolvePhones 按 4 层优先级拼装电话号码列表
//
//	1. 值班表匹配：取 primary_phone + backup_phone
//	2. 否则按 group_name 匹配 server_groups
//	3. 否则用 default_phones
//	4. 最后合并 always_phones → 去重返回
func ResolvePhones(
	event *Event,
	scheduleEntry *OncallEntry,
	groups map[string][]string, // group_name → phones
	alwaysPhones []string,
	defaultPhones []string,
) []string {
	var phones []string

	if scheduleEntry != nil {
		if scheduleEntry.PrimaryPhone != "" {
			phones = append(phones, scheduleEntry.PrimaryPhone)
		}
		if scheduleEntry.BackupPhone != "" {
			phones = append(phones, scheduleEntry.BackupPhone)
		}
	} else if event.GroupName != "" {
		if groupPhones, ok := groups[event.GroupName]; ok {
			phones = append(phones, groupPhones...)
		}
	}

	// 兜底
	if len(phones) == 0 {
		phones = append(phones, defaultPhones...)
	}

	// 始终额外拨打 always_phones
	phones = append(phones, alwaysPhones...)

	return dedupe(phones)
}

// SeverityLabel 严重等级 → 中文标签
func SeverityLabel(level int) string {
	switch level {
	case 1:
		return "严重"
	case 2:
		return "警告"
	case 3:
		return "提醒"
	default:
		return "未知"
	}
}

// BuildFallbackHash 当夜莺传入的 hash 为空时，用关键字段生成确定性 hash
// 对 rule_name + group_name + tags_map 做 SHA256
func BuildFallbackHash(event *Event) string {
	var sb strings.Builder
	sb.WriteString(event.RuleName)
	sb.WriteString("|")
	sb.WriteString(event.GroupName)
	sb.WriteString("|")

	// tags_map 按 key 排序保证确定性
	keys := make([]string, 0, len(event.TagsMap))
	for k := range event.TagsMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(event.TagsMap[k])
		sb.WriteString(";")
	}

	h := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("%x", h)
}

// EffectiveHash 返回有效 hash：优先用夜莺自带 hash，为空时用 fallback
func EffectiveHash(event *Event) string {
	if event.Hash != "" {
		return event.Hash
	}
	return BuildFallbackHash(event)
}

// dedupe 字符串切片去重，保持顺序
func dedupe(slice []string) []string {
	seen := make(map[string]bool, len(slice))
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s == "" {
			continue
		}
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
