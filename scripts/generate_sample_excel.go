package main

import (
	"log"
	"os"

	"github.com/xuri/excelize/v2"
)

func main() {
	if err := os.MkdirAll("examples", 0755); err != nil {
		log.Fatal(err)
	}
	book := excelize.NewFile()
	sheet := book.GetSheetName(0)
	_ = book.SetSheetName(sheet, "销售明细")
	sheet = "销售明细"
	rows := [][]interface{}{
		{"订单日期", "商品分类", "商品名称", "销售额", "订单量", "地区"},
		{"2026-07-01", "数码", "无线耳机", 1299.00, 13, "上海"},
		{"2026-07-02", "家居", "桌面台灯", 899.00, 10, "北京"},
		{"2026-07-03", "数码", "机械键盘", 1688.00, 8, "广州"},
		{"2026-07-04", "服饰", "防晒外套", 756.00, 12, "杭州"},
		{"2026-07-05", "家居", "收纳套装", 1020.00, 15, "成都"},
	}
	for rowIndex, row := range rows {
		for columnIndex, value := range row {
			cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
			if err := book.SetCellValue(sheet, cell, value); err != nil {
				log.Fatal(err)
			}
		}
	}
	if err := book.SaveAs("examples/销售数据示例.xlsx"); err != nil {
		log.Fatal(err)
	}
}
