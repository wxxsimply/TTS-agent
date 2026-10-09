package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tts-agent/internal/domain"
)

type Metric struct {
	Label        string      `json:"label"`
	Value        interface{} `json:"value"`
	DisplayValue string      `json:"display_value"`
	Unit         string      `json:"unit,omitempty"`
}

type DataScope struct {
	TimeRange string `json:"time_range,omitempty"`
	RowCount  int    `json:"row_count"`
}

type Report struct {
	Title            string            `json:"title"`
	Conclusion       string            `json:"conclusion"`
	Markdown         string            `json:"markdown,omitempty"`
	Metrics          []Metric          `json:"metrics"`
	Insights         []string          `json:"insights"`
	DataScope        DataScope         `json:"data_scope"`
	Caveats          []string          `json:"caveats"`
	ColumnLabels     map[string]string `json:"column_labels"`
	ColumnFormats    map[string]string `json:"column_formats"`
	SensitiveColumns map[string]bool   `json:"sensitive_columns,omitempty"`
}

type reportNarrative struct {
	Conclusion string   `json:"conclusion"`
	Insights   []string `json:"insights"`
	Caveats    []string `json:"caveats"`
}

func buildBaseReport(query string, rows []map[string]interface{}, dict *domain.Dictionary) Report {
	report := Report{
		Title:            truncateRunes(query, 32),
		Conclusion:       fmt.Sprintf("已查询到 %d 条数据。", len(rows)),
		Metrics:          []Metric{},
		Insights:         []string{},
		Caveats:          []string{},
		DataScope:        DataScope{RowCount: len(rows), TimeRange: detectTimeRange(rows)},
		ColumnLabels:     map[string]string{},
		ColumnFormats:    map[string]string{},
		SensitiveColumns: map[string]bool{},
	}
	if len(rows) == 0 {
		report.Conclusion = "当前条件下没有查询到数据。"
		report.Caveats = append(report.Caveats, "可以尝试扩大时间范围或调整筛选条件。")
		report.Markdown = buildReportMarkdown(report)
		return report
	}

	columns := sortedColumns(rows[0])
	for _, column := range columns {
		field := dict.Field(column)
		report.ColumnLabels[column] = field.Label
		report.ColumnFormats[column] = field.Format
		report.SensitiveColumns[column] = field.Sensitive
	}

	if len(rows) == 1 {
		for _, column := range columns {
			if len(report.Metrics) >= 4 {
				break
			}
			value := rows[0][column]
			if !isMetricValue(value) {
				continue
			}
			field := dict.Field(column)
			report.Metrics = append(report.Metrics, Metric{
				Label: field.Label, Value: value,
				DisplayValue: formatDisplayValue(value, field.Format, field.Unit), Unit: field.Unit,
			})
		}
	}
	if len(report.Metrics) == 0 {
		report.Metrics = append(report.Metrics, Metric{
			Label: "数据记录", Value: len(rows), DisplayValue: fmt.Sprintf("%d 条", len(rows)), Unit: "条",
		})
	}
	if len(rows) >= 1000 {
		report.Caveats = append(report.Caveats, "为保障查询性能，本次结果最多返回 1000 条记录。")
	}
	report.Markdown = buildReportMarkdown(report)
	return report
}

func applyNarrative(report Report, raw string) Report {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	var narrative reportNarrative
	if err := json.Unmarshal([]byte(cleaned), &narrative); err != nil {
		report.Markdown = buildReportMarkdown(report)
		return report
	}
	if strings.TrimSpace(narrative.Conclusion) != "" {
		report.Conclusion = strings.TrimSpace(narrative.Conclusion)
	}
	if len(narrative.Insights) > 3 {
		narrative.Insights = narrative.Insights[:3]
	}
	report.Insights = nonEmptyStrings(narrative.Insights)
	report.Caveats = append(report.Caveats, nonEmptyStrings(narrative.Caveats)...)
	report.Markdown = buildReportMarkdown(report)
	return report
}

func applyMarkdownNarrative(report Report, raw string) Report {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```markdown")
	cleaned = strings.TrimPrefix(cleaned, "```md")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		report.Markdown = buildReportMarkdown(report)
		return report
	}

	section := ""
	var conclusions, insights, caveats []string
	for _, rawLine := range strings.Split(cleaned, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			switch {
			case strings.Contains(heading, "结论"):
				section = "conclusion"
			case strings.Contains(heading, "发现"), strings.Contains(heading, "洞察"):
				section = "insights"
			case strings.Contains(heading, "说明"), strings.Contains(heading, "限制"):
				section = "caveats"
			}
			continue
		}
		item := strings.TrimSpace(strings.TrimLeft(line, "-*+0123456789. "))
		if item == "" {
			continue
		}
		switch section {
		case "insights":
			insights = append(insights, item)
		case "caveats":
			caveats = append(caveats, item)
		default:
			conclusions = append(conclusions, item)
		}
	}
	if len(conclusions) > 0 {
		report.Conclusion = strings.Join(conclusions, " ")
	}
	if len(insights) > 3 {
		insights = insights[:3]
	}
	report.Insights = nonEmptyStrings(insights)
	report.Caveats = append(report.Caveats, nonEmptyStrings(caveats)...)
	report.Markdown = buildReportMarkdown(report)
	return report
}

func buildReportMarkdown(report Report) string {
	var content strings.Builder
	content.WriteString("## 结论\n\n")
	content.WriteString(strings.TrimSpace(report.Conclusion))
	if len(report.Insights) > 0 {
		content.WriteString("\n\n## 关键发现\n\n")
		for _, insight := range report.Insights {
			content.WriteString("- " + strings.TrimSpace(insight) + "\n")
		}
	}
	if len(report.Caveats) > 0 {
		content.WriteString("\n## 数据说明\n\n")
		for _, caveat := range report.Caveats {
			content.WriteString("- " + strings.TrimSpace(caveat) + "\n")
		}
	}
	return strings.TrimSpace(content.String())
}

func sortedColumns(row map[string]interface{}) []string {
	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	return columns
}

func isMetricValue(value interface{}) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return true
	default:
		return false
	}
}

func numericValue(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		n, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		return n, err == nil
	}
}

func formatDisplayValue(value interface{}, format, unit string) string {
	if format == "boolean" {
		if v, ok := value.(bool); ok {
			if v {
				return "是"
			}
			return "否"
		}
	}
	n, ok := numericValue(value)
	if !ok {
		return fmt.Sprint(value)
	}
	switch format {
	case "percent":
		if math.Abs(n) <= 1 {
			n *= 100
		}
		return fmt.Sprintf("%.2f%%", n)
	case "currency":
		return fmt.Sprintf("%s %s", formatNumber(n, 2), unit)
	case "integer":
		return fmt.Sprintf("%s %s", formatNumber(n, 0), unit)
	default:
		return strings.TrimSpace(fmt.Sprintf("%s %s", formatNumber(n, 2), unit))
	}
}

func formatNumber(value float64, decimals int) string {
	raw := strconv.FormatFloat(value, 'f', decimals, 64)
	parts := strings.SplitN(raw, ".", 2)
	integer := parts[0]
	sign := ""
	if strings.HasPrefix(integer, "-") {
		sign = "-"
		integer = strings.TrimPrefix(integer, "-")
	}
	for i := len(integer) - 3; i > 0; i -= 3 {
		integer = integer[:i] + "," + integer[i:]
	}
	if len(parts) == 2 {
		return sign + integer + "." + parts[1]
	}
	return sign + integer
}

func detectTimeRange(rows []map[string]interface{}) string {
	var dates []time.Time
	for _, row := range rows {
		for column, value := range row {
			key := strings.ToLower(column)
			if !strings.Contains(key, "date") && !strings.Contains(key, "time") && key != "day" && key != "month" {
				continue
			}
			if parsed, ok := parseDate(value); ok {
				dates = append(dates, parsed)
			}
		}
	}
	if len(dates) == 0 {
		return ""
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	start, end := dates[0].Format("2006-01-02"), dates[len(dates)-1].Format("2006-01-02")
	if start == end {
		return start
	}
	return start + " 至 " + end
}

func parseDate(value interface{}) (time.Time, bool) {
	if value == nil {
		return time.Time{}, false
	}
	if date, ok := value.(time.Time); ok {
		return date, true
	}
	s := fmt.Sprint(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02", "2006-01"} {
		if date, err := time.Parse(layout, s); err == nil {
			return date, true
		}
	}
	return time.Time{}, false
}

func truncateRunes(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
