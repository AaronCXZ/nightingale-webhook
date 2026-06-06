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

var phonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)
var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ParseCSV 新 CSV 格式：type,date,group_name,name,phone
// type=primary: group_name 不起作用（可填 main 或空），name+phone 为主值
// type=backup: group_name+name+phone 为备值
func ParseCSV(reader io.Reader) ([]store.OncallPrimary, []store.OncallBackup, error) {
	r := csv.NewReader(reader)
	r.TrimLeadingSpace = true

	var primaries []store.OncallPrimary
	var backups []store.OncallBackup
	lineNum := 0

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("第 %d 行 CSV 解析错误: %w", lineNum+1, err)
		}
		lineNum++

		if len(record) == 0 || (len(record) == 1 && strings.TrimSpace(record[0]) == "") {
			continue
		}

		// 检测并跳过 header 行
		if lineNum == 1 && len(record) >= 1 {
			first := strings.TrimSpace(record[0])
			if strings.EqualFold(first, "type") {
				continue
			}
		}

		if len(record) < 5 {
			return nil, nil, fmt.Errorf("第 %d 行字段不足，需要 5 列 (type,date,group_name,name,phone)，got %d 列", lineNum, len(record))
		}

		recType := strings.TrimSpace(strings.ToLower(record[0]))
		date := strings.TrimSpace(record[1])
		groupName := strings.TrimSpace(record[2])
		name := strings.TrimSpace(record[3])
		phone := strings.TrimSpace(record[4])

		today := time.Now().UTC().Format("2006-01-02")
		if date < today {
			return nil, nil, fmt.Errorf("第 %d 行: 不能导入过去的日期 %s（今天=%s）", lineNum, date, today)
		}
		if !datePattern.MatchString(date) {
			return nil, nil, fmt.Errorf("第 %d 行: 日期格式错误 %s", lineNum, date)
		}
		if phone == "" {
			return nil, nil, fmt.Errorf("第 %d 行: phone 不能为空", lineNum)
		}
		if !phonePattern.MatchString(phone) {
			return nil, nil, fmt.Errorf("第 %d 行: phone 格式错误: %s", lineNum, phone)
		}

		switch recType {
		case "primary":
			primaries = append(primaries, store.OncallPrimary{Date: date, Name: name, Phone: phone})
		case "backup":
			if groupName == "" {
				return nil, nil, fmt.Errorf("第 %d 行: backup 类型 group_name 不能为空", lineNum)
			}
			backups = append(backups, store.OncallBackup{Date: date, GroupName: groupName, Name: name, Phone: phone})
		default:
			return nil, nil, fmt.Errorf("第 %d 行: 无效类型 %q，应为 primary 或 backup", lineNum, recType)
		}
	}
	return primaries, backups, nil
}

// ValidateDateNotPast 检查日期是否不是过去日期
func ValidateDateNotPast(date string) error {
	today := time.Now().UTC().Format("2006-01-02")
	if date < today {
		return fmt.Errorf("不能修改已过去的排班: %s", date)
	}
	return nil
}

// ExportCSV 导出新 CSV 格式
func ExportCSV(days []store.CalendarDay) ([]byte, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)

	if err := w.Write([]string{"type", "date", "group_name", "name", "phone"}); err != nil {
		return nil, err
	}

	for _, d := range days {
		if d.PrimaryName != "" {
			if err := w.Write([]string{"primary", d.Date, "main", d.PrimaryName, d.PrimaryPhone}); err != nil {
				return nil, err
			}
		}
		for _, b := range d.Backups {
			if err := w.Write([]string{"backup", b.Date, b.GroupName, b.Name, b.Phone}); err != nil {
				return nil, err
			}
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}
