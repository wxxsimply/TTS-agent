package api

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/tts-agent/internal/agent"
	"github.com/xuri/excelize/v2"
)

func TestBuildExcelExportsAllRowsWithChineseHeaders(t *testing.T) {
	rows := make([]map[string]interface{}, 25)
	for i := range rows {
		rows[i] = map[string]interface{}{
			"email":       fmt.Sprintf("customer-%d@example.com", i),
			"total_sales": float64(i) * 10,
		}
	}
	report := agent.Report{
		Title:      "客户销售额",
		Conclusion: "共查询到 25 条数据。",
		DataScope:  agent.DataScope{RowCount: 25},
		ColumnLabels: map[string]string{
			"email": "客户邮箱", "total_sales": "销售额",
		},
		SensitiveColumns: map[string]bool{"email": true},
	}

	workbook, err := excelize.OpenReader(bytes.NewReader(buildExcel(rows, report)))
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	exported, err := workbook.GetRows("数据明细")
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != 26 {
		t.Fatalf("expected header plus 25 rows, got %d", len(exported))
	}
	if exported[0][0] != "客户邮箱" {
		t.Fatalf("unexpected header: %q", exported[0][0])
	}
	if exported[1][0] == rows[0]["email"] {
		t.Fatal("sensitive email was exported without masking")
	}
}
