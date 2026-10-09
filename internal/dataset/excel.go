package dataset

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tts-agent/internal/store"
	"github.com/xuri/excelize/v2"
)

const MaxRows = 50000
const MaxColumns = 100
const PreviewRowCount = 10

var identifierChars = regexp.MustCompile(`[^a-z0-9]+`)

var allowedColumnTypes = map[string]struct{}{
	"BIGINT":           {},
	"DOUBLE PRECISION": {},
	"BOOLEAN":          {},
	"TIMESTAMPTZ":      {},
	"TEXT":             {},
}

type Preview struct {
	Dataset    *store.Dataset `json:"dataset"`
	SampleRows [][]string     `json:"sample_rows"`
	Warnings   []string       `json:"warnings,omitempty"`
}

type CellError struct {
	Row    int    `json:"row"`
	Column string `json:"column"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

type ValidationError struct {
	Errors []CellError
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "Excel 数据格式不正确"
	}
	first := e.Errors[0]
	return fmt.Sprintf("第 %d 行“%s”的值“%s”无法按%s导入", first.Row, first.Column, first.Value, first.Reason)
}

func ParseExcel(reader io.Reader, fileName string, userID uuid.UUID) (*store.Dataset, [][]interface{}, error) {
	return parseExcel(reader, fileName, userID, nil)
}

func ParseExcelWithColumnTypes(reader io.Reader, fileName string, userID uuid.UUID, columnTypes map[string]string) (*store.Dataset, [][]interface{}, error) {
	return parseExcel(reader, fileName, userID, columnTypes)
}

func PreviewExcel(reader io.Reader, fileName string, userID uuid.UUID) (*Preview, error) {
	dataset, sourceRows, _, emptyRows, err := parseWorkbook(reader, fileName, userID, nil, false)
	if err != nil {
		return nil, err
	}
	sampleSize := len(sourceRows)
	if sampleSize > PreviewRowCount {
		sampleSize = PreviewRowCount
	}
	warnings := make([]string, 0, 1)
	if emptyRows > 0 {
		warnings = append(warnings, fmt.Sprintf("已忽略 %d 行完全为空的数据", emptyRows))
	}
	return &Preview{Dataset: dataset, SampleRows: sourceRows[:sampleSize], Warnings: warnings}, nil
}

func parseExcel(reader io.Reader, fileName string, userID uuid.UUID, columnTypes map[string]string) (*store.Dataset, [][]interface{}, error) {
	dataset, sourceRows, _, _, err := parseWorkbook(reader, fileName, userID, columnTypes, true)
	if err != nil {
		return nil, nil, err
	}
	values := make([][]interface{}, 0, len(sourceRows))
	cellErrors := make([]CellError, 0)
	for rowIndex, sourceRow := range sourceRows {
		row := make([]interface{}, len(dataset.Columns))
		for columnIndex, column := range dataset.Columns {
			value := ""
			if columnIndex < len(sourceRow) {
				value = strings.TrimSpace(sourceRow[columnIndex])
			}
			converted, err := convertValue(value, column.Type)
			if err != nil {
				cellErrors = append(cellErrors, CellError{Row: rowIndex + 2, Column: column.Label, Value: value, Reason: displayType(column.Type)})
				if len(cellErrors) >= 20 {
					break
				}
			}
			row[columnIndex] = converted
		}
		if len(cellErrors) >= 20 {
			break
		}
		values = append(values, row)
	}
	if len(cellErrors) > 0 {
		return nil, nil, &ValidationError{Errors: cellErrors}
	}
	return dataset, values, nil
}

func parseWorkbook(reader io.Reader, fileName string, userID uuid.UUID, columnTypes map[string]string, validateColumnTypes bool) (*store.Dataset, [][]string, [][]string, int, error) {
	book, err := excelize.OpenReader(reader)
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("无法读取 Excel，请确认文件没有损坏或加密")
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil, nil, 0, fmt.Errorf("Excel 中没有可读取的工作表")
	}
	sheet := sheets[0]
	rows, err := book.GetRows(sheet)
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("读取工作表失败")
	}
	if len(rows) < 2 {
		return nil, nil, nil, 0, fmt.Errorf("Excel 至少需要一行表头和一行数据")
	}
	headers := rows[0]
	if len(headers) == 0 || len(headers) > MaxColumns {
		return nil, nil, nil, 0, fmt.Errorf("Excel 列数必须在 1 到 %d 之间", MaxColumns)
	}
	if len(rows)-1 > MaxRows {
		return nil, nil, nil, 0, fmt.Errorf("单次最多导入 %d 行数据", MaxRows)
	}

	columns := buildColumns(headers, rows[1:])
	if validateColumnTypes {
		if err := applyColumnTypes(columns, columnTypes); err != nil {
			return nil, nil, nil, 0, err
		}
	}
	sourceRows := make([][]string, 0, len(rows)-1)
	emptyRows := 0
	for _, sourceRow := range rows[1:] {
		if rowEmpty(sourceRow) {
			emptyRows++
			continue
		}
		sourceRows = append(sourceRows, sourceRow)
	}
	if len(sourceRows) == 0 {
		return nil, nil, nil, 0, fmt.Errorf("Excel 中没有可导入的数据")
	}
	id := uuid.New()
	name := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	if strings.TrimSpace(name) == "" {
		name = "导入数据"
	}
	dataset := &store.Dataset{ID: id, UserID: userID, Name: name, SourceFile: filepath.Base(fileName), SheetName: sheet, TableName: "dataset_" + strings.ReplaceAll(id.String(), "-", ""), Columns: columns, RowCount: len(sourceRows), CreatedAt: time.Now().UTC()}
	return dataset, sourceRows, rows[1:], emptyRows, nil
}

func applyColumnTypes(columns []store.DatasetColumn, columnTypes map[string]string) error {
	for name, typeName := range columnTypes {
		matched := false
		for index := range columns {
			if columns[index].Name != name {
				continue
			}
			matched = true
			normalized := strings.ToUpper(strings.TrimSpace(typeName))
			if _, allowed := allowedColumnTypes[normalized]; !allowed {
				return fmt.Errorf("字段“%s”的类型不受支持", columns[index].Label)
			}
			columns[index].Type = normalized
			break
		}
		if !matched {
			return fmt.Errorf("字段类型设置中包含不存在的字段“%s”", name)
		}
	}
	return nil
}

func buildColumns(headers []string, rows [][]string) []store.DatasetColumn {
	used := map[string]int{}
	columns := make([]store.DatasetColumn, len(headers))
	for i, header := range headers {
		label := strings.TrimSpace(header)
		if label == "" {
			label = fmt.Sprintf("第%d列", i+1)
		}
		base := strings.Trim(identifierChars.ReplaceAllString(strings.ToLower(label), "_"), "_")
		if base == "" || base[0] >= '0' && base[0] <= '9' {
			base = fmt.Sprintf("col_%d", i+1)
		}
		name := base
		used[base]++
		if used[base] > 1 {
			name = fmt.Sprintf("%s_%d", base, used[base])
		}
		columns[i] = store.DatasetColumn{Name: name, Label: label, Type: inferType(rows, i)}
	}
	return columns
}

func inferType(rows [][]string, index int) string {
	typeName := ""
	limit := len(rows)
	if limit > 1000 {
		limit = 1000
	}
	for _, row := range rows[:limit] {
		if index >= len(row) || strings.TrimSpace(row[index]) == "" {
			continue
		}
		candidate := valueType(strings.TrimSpace(row[index]))
		if typeName == "" {
			typeName = candidate
			continue
		}
		if typeName == "BIGINT" && candidate == "DOUBLE PRECISION" {
			typeName = candidate
			continue
		}
		if typeName == "DOUBLE PRECISION" && candidate == "BIGINT" {
			continue
		}
		if typeName != candidate {
			return "TEXT"
		}
	}
	if typeName == "" {
		return "TEXT"
	}
	return typeName
}

func valueType(value string) string {
	normalized, _ := normalizeNumber(value)
	if _, err := strconv.ParseInt(normalized, 10, 64); err == nil {
		return "BIGINT"
	}
	if _, err := strconv.ParseFloat(normalized, 64); err == nil {
		return "DOUBLE PRECISION"
	}
	lower := strings.ToLower(value)
	if lower == "true" || lower == "false" || value == "是" || value == "否" {
		return "BOOLEAN"
	}
	if _, ok := parseDate(value); ok {
		return "TIMESTAMPTZ"
	}
	return "TEXT"
}

func convertValue(value, typeName string) (interface{}, error) {
	if value == "" {
		return nil, nil
	}
	switch typeName {
	case "BIGINT":
		normalized, _ := normalizeNumber(value)
		parsed, err := strconv.ParseInt(normalized, 10, 64)
		return parsed, err
	case "DOUBLE PRECISION":
		normalized, percent := normalizeNumber(value)
		parsed, err := strconv.ParseFloat(normalized, 64)
		if err != nil {
			return nil, err
		}
		if percent {
			parsed /= 100
		}
		return parsed, nil
	case "BOOLEAN":
		lower := strings.ToLower(value)
		if lower != "true" && lower != "false" && value != "是" && value != "否" {
			return nil, fmt.Errorf("invalid boolean")
		}
		return strings.EqualFold(value, "true") || value == "是", nil
	case "TIMESTAMPTZ":
		parsed, ok := parseDate(value)
		if !ok {
			return nil, fmt.Errorf("invalid date")
		}
		return parsed, nil
	default:
		return value, nil
	}
}

// normalizeNumber accepts the common display forms returned by Excel readers.
// Excelize returns formatted cell text, so a numeric currency cell may arrive
// here as "$1,234.50" instead of its raw numeric value.
func normalizeNumber(value string) (string, bool) {
	normalized := strings.TrimSpace(value)
	percent := strings.HasSuffix(normalized, "%")
	if percent {
		normalized = strings.TrimSuffix(normalized, "%")
	}
	normalized = strings.NewReplacer(
		",", "",
		"$", "",
		"¥", "",
		"￥", "",
		"元", "",
		"USD", "",
		"usd", "",
	).Replace(normalized)
	normalized = strings.TrimSpace(normalized)
	if strings.HasPrefix(normalized, "(") && strings.HasSuffix(normalized, ")") {
		normalized = "-" + strings.TrimSpace(normalized[1:len(normalized)-1])
	}
	return normalized, percent
}

func displayType(typeName string) string {
	switch typeName {
	case "BIGINT":
		return "整数"
	case "DOUBLE PRECISION":
		return "数值"
	case "BOOLEAN":
		return "是/否"
	case "TIMESTAMPTZ":
		return "日期时间"
	default:
		return "文本"
	}
}

func parseDate(value string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02", "2006/01/02 15:04:05", "2006/01/02", "2006年1月2日"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func rowEmpty(row []string) bool {
	for _, value := range row {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}
