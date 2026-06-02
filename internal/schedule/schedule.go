package schedule

import (
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"webhook/internal/store"
)

// 手机号格式校验（中国大陆手机号）
var phonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

// 日期格式 YYYY-MM-DD
var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ParseCSV 解析值班表 CSV 文件
// CSV 格式：group_name,date,primary_name,primary_phone,backup_name,backup_phone
func ParseCSV(reader io.Reader) ([]store.OncallEntry, error) {
	r := csv.NewReader(reader)
	r.TrimLeadingSpace = true

	// 自动检测是否有 header 行
	var entries []store.OncallEntry
	lineNum := 0

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("第 %d 行 CSV 解析错误: %w", lineNum+1, err)
		}
		lineNum++

		// 跳过空行
		if len(record) == 0 || (len(record) == 1 && strings.TrimSpace(record[0]) == "") {
			continue
		}

		// 检测并跳过 header 行
		if lineNum == 1 && len(record) >= 1 {
			first := strings.TrimSpace(record[0])
			if strings.EqualFold(first, "group_name") || strings.EqualFold(first, "group name") {
				continue
			}
		}

		if len(record) < 4 {
			return nil, fmt.Errorf("第 %d 行字段不足，至少需要 4 列 (group_name, date, primary_phone)，got %d 列", lineNum, len(record))
		}

		entry := store.OncallEntry{
			GroupName:    strings.TrimSpace(record[0]),
			Date:         strings.TrimSpace(record[1]),
			PrimaryName:  strings.TrimSpace(getCol(record, 2)),
			PrimaryPhone: strings.TrimSpace(record[3]),
			BackupName:   strings.TrimSpace(getCol(record, 4)),
			BackupPhone:  strings.TrimSpace(getCol(record, 5)),
		}

		entries = append(entries, entry)
	}

	// 校验
	for i, e := range entries {
		if err := validateEntry(e); err != nil {
			return nil, fmt.Errorf("第 %d 行校验失败: %w", i+1, err)
		}
	}

	return entries, nil
}

func getCol(record []string, idx int) string {
	if idx < len(record) {
		return record[idx]
	}
	return ""
}

// validateEntry 校验单条值班表
func validateEntry(e store.OncallEntry) error {
	if e.GroupName == "" {
		return fmt.Errorf("group_name 不能为空")
	}
	if e.Date == "" {
		return fmt.Errorf("date 不能为空")
	}
	if !datePattern.MatchString(e.Date) {
		return fmt.Errorf("日期格式错误，应为 YYYY-MM-DD: %s", e.Date)
	}
	if e.PrimaryPhone == "" {
		return fmt.Errorf("primary_phone 不能为空")
	}
	if !phonePattern.MatchString(e.PrimaryPhone) {
		return fmt.Errorf("primary_phone 格式错误: %s", e.PrimaryPhone)
	}
	if e.BackupPhone != "" && !phonePattern.MatchString(e.BackupPhone) {
		return fmt.Errorf("backup_phone 格式错误: %s", e.BackupPhone)
	}

	// 检查日期不能是过去日期
	today := time.Now().UTC().Format("2006-01-02")
	if e.Date < today {
		return fmt.Errorf("不能导入过去的日期 %s（今天=%s）", e.Date, today)
	}

	return nil
}

// ValidateDateNotPast 检查日期是否不是过去日期（编辑/删除时使用）
func ValidateDateNotPast(date string) error {
	today := time.Now().UTC().Format("2006-01-02")
	if date < today {
		return fmt.Errorf("不能修改已过去的排班: %s", date)
	}
	return nil
}

// ExportCSV 将值班表序列化为 CSV 格式
func ExportCSV(entries []store.OncallEntry) ([]byte, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)

	// 写 header
	if err := w.Write([]string{"group_name", "date", "primary_name", "primary_phone", "backup_name", "backup_phone"}); err != nil {
		return nil, err
	}

	for _, e := range entries {
		if err := w.Write([]string{
			e.GroupName,
			e.Date,
			e.PrimaryName,
			e.PrimaryPhone,
			e.BackupName,
			e.BackupPhone,
		}); err != nil {
			return nil, err
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}

	return []byte(buf.String()), nil
}
