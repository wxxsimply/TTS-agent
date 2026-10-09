package api

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/tts-agent/internal/agent"
	"github.com/xuri/excelize/v2"
)

func buildExcel(rows []map[string]interface{}, report agent.Report) []byte {
	f := excelize.NewFile()
	detailSheet := "数据明细"
	noteSheet := "查询说明"
	_, _ = f.NewSheet(detailSheet)
	_, _ = f.NewSheet(noteSheet)
	_ = f.DeleteSheet("Sheet1")
	f.SetActiveSheet(0)

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"1677FF"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})

	if len(rows) > 0 {
		columns := make([]string, 0, len(rows[0]))
		for column := range rows[0] {
			columns = append(columns, column)
		}
		sort.Strings(columns)
		for i, column := range columns {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			label := report.ColumnLabels[column]
			if label == "" {
				label = column
			}
			_ = f.SetCellValue(detailSheet, cell, label)
			_ = f.SetCellStyle(detailSheet, cell, cell, headerStyle)
			_ = f.SetColWidth(detailSheet, columnName(i+1), columnName(i+1), 18)
		}
		for rowIndex, row := range rows {
			for columnIndex, column := range columns {
				cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+2)
				value := row[column]
				if report.SensitiveColumns[column] {
					value = maskSensitiveValue(value)
				}
				_ = f.SetCellValue(detailSheet, cell, value)
			}
		}
		lastCell, _ := excelize.CoordinatesToCellName(len(columns), len(rows)+1)
		_ = f.AutoFilter(detailSheet, "A1:"+lastCell, []excelize.AutoFilterOptions{})
		_ = f.SetPanes(detailSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	}

	notes := [][]interface{}{
		{"报告主题", report.Title},
		{"结论摘要", report.Conclusion},
		{"统计时间", report.DataScope.TimeRange},
		{"数据行数", len(rows)},
		{"生成时间", chinaNow().Format("2006-01-02 15:04:05")},
	}
	for i, note := range notes {
		_ = f.SetCellValue(noteSheet, fmt.Sprintf("A%d", i+1), note[0])
		_ = f.SetCellValue(noteSheet, fmt.Sprintf("B%d", i+1), note[1])
	}
	_ = f.SetColWidth(noteSheet, "A", "A", 16)
	_ = f.SetColWidth(noteSheet, "B", "B", 72)
	_ = f.SetCellStyle(noteSheet, "A1", fmt.Sprintf("A%d", len(notes)), headerStyle)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return []byte(fmt.Sprintf("error: %v", err))
	}
	return buf.Bytes()
}

func maskSensitiveValue(value interface{}) string {
	runes := []rune(fmt.Sprint(value))
	if len(runes) <= 4 {
		return "****"
	}
	if len(runes) <= 8 {
		return string(runes[:2]) + "****"
	}
	return string(runes[:3]) + "****" + string(runes[len(runes)-3:])
}

func columnName(index int) string {
	name, _ := excelize.ColumnNumberToName(index)
	return name
}

func exportFileName(title string) string {
	cleaned := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "").Replace(strings.TrimSpace(title))
	if cleaned == "" {
		cleaned = "数据分析报告"
	}
	runes := []rune(cleaned)
	if len(runes) > 24 {
		cleaned = string(runes[:24])
	}
	return url.PathEscape(cleaned + "_" + chinaNow().Format("20060102") + ".xlsx")
}

func chinaNow() time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*60*60)
	}
	return time.Now().In(location)
}
