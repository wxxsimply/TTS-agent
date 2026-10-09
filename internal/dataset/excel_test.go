package dataset

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

func TestParseExcelInfersColumnsAndRows(t *testing.T) {
	book := excelize.NewFile()
	sheet := book.GetSheetName(0)
	data := [][]interface{}{
		{"订单号", "amount", "下单日期"},
		{"1001", "88.50", "2026-07-01"},
		{"1002", "120.00", "2026-07-02"},
	}
	for rowIndex, row := range data {
		for columnIndex, value := range row {
			cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err := book.SetCellValue(sheet, cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		t.Fatal(err)
	}

	parsed, rows, err := ParseExcel(&buffer, "销售明细.xlsx", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "销售明细" || parsed.RowCount != 2 {
		t.Fatalf("unexpected dataset: %+v", parsed)
	}
	if len(parsed.Columns) != 3 || parsed.Columns[0].Name != "col_1" {
		t.Fatalf("unexpected columns: %+v", parsed.Columns)
	}
	if parsed.Columns[0].Type != "BIGINT" || parsed.Columns[1].Type != "DOUBLE PRECISION" || parsed.Columns[2].Type != "TIMESTAMPTZ" {
		t.Fatalf("unexpected inferred types: %+v", parsed.Columns)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestParseExcelRejectsEmptyWorkbook(t *testing.T) {
	book := excelize.NewFile()
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseExcel(&buffer, "empty.xlsx", uuid.New()); err == nil {
		t.Fatal("expected empty workbook error")
	}
}

func TestPreviewExcelReturnsRowsAndWarnings(t *testing.T) {
	book := excelize.NewFile()
	sheet := book.GetSheetName(0)
	rows := [][]interface{}{
		{"订单号", "金额"},
		{"001", "88.50"},
		{"", ""},
		{"002", "120.00"},
	}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err := book.SetCellValue(sheet, cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		t.Fatal(err)
	}

	preview, err := PreviewExcel(&buffer, "订单.xlsx", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Dataset.RowCount != 2 || len(preview.SampleRows) != 2 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if len(preview.Warnings) != 1 {
		t.Fatalf("expected an empty-row warning, got %+v", preview.Warnings)
	}
}

func TestParseExcelAppliesColumnTypeOverrides(t *testing.T) {
	book := excelize.NewFile()
	sheet := book.GetSheetName(0)
	rows := [][]interface{}{{"订单号", "金额"}, {"001", "88.50"}}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err := book.SetCellValue(sheet, cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		t.Fatal(err)
	}

	parsed, values, err := ParseExcelWithColumnTypes(&buffer, "订单.xlsx", uuid.New(), map[string]string{"col_1": "TEXT"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Columns[0].Type != "TEXT" || values[0][0] != "001" {
		t.Fatalf("override was not applied: columns=%+v values=%+v", parsed.Columns, values)
	}
}

func TestParseExcelReportsInvalidOverrideValue(t *testing.T) {
	book := excelize.NewFile()
	sheet := book.GetSheetName(0)
	if err := book.SetSheetRow(sheet, "A1", &[]interface{}{"订单号"}); err != nil {
		t.Fatal(err)
	}
	if err := book.SetSheetRow(sheet, "A2", &[]interface{}{"A-001"}); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		t.Fatal(err)
	}

	_, _, err := ParseExcelWithColumnTypes(&buffer, "订单.xlsx", uuid.New(), map[string]string{"col_1": "BIGINT"})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Errors) != 1 {
		t.Fatalf("expected cell validation error, got %v", err)
	}
}

func TestCurrencyAndPercentCellsAreImportedAsNumbers(t *testing.T) {
	if valueType("$1,234.50") != "DOUBLE PRECISION" {
		t.Fatal("currency should be inferred as a decimal")
	}
	if valueType("12.5%") != "DOUBLE PRECISION" {
		t.Fatal("percent should be inferred as a decimal")
	}
	currency, err := convertValue("$1,234.50", "DOUBLE PRECISION")
	if err != nil || currency != 1234.5 {
		t.Fatalf("unexpected currency conversion: value=%v err=%v", currency, err)
	}
	percent, err := convertValue("12.5%", "DOUBLE PRECISION")
	if err != nil || percent != 0.125 {
		t.Fatalf("unexpected percent conversion: value=%v err=%v", percent, err)
	}
}
