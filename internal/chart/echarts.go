package chart

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func BuildChartConfig(rows []map[string]interface{}, query string, labels map[string]string) map[string]interface{} {
	if len(rows) == 0 {
		return nil
	}
	columns := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		if !isInternalColumn(k) {
			columns = append(columns, k)
		}
	}
	sort.Strings(columns)
	if len(columns) < 2 {
		return nil
	}
	colTypes := map[string]string{}
	for _, c := range columns {
		colTypes[c] = inferType(rows, c)
	}
	var timeCol string
	for _, c := range columns {
		if colTypes[c] == "date" {
			timeCol = c
			break
		}
	}
	var numericCols, categoryCols []string
	for _, c := range columns {
		switch colTypes[c] {
		case "number":
			numericCols = append(numericCols, c)
		case "string":
			categoryCols = append(categoryCols, c)
		}
	}
	if timeCol != "" {
		// Detail data often repeats each order date many times. Rendering that as a
		// line chart creates a misleading wall of vertical lines, so only chart an
		// already aggregated time series.
		if len(numericCols) > 0 && isAggregatedTimeSeries(rows, timeCol) {
			return lineChart(timeCol, preferredNumericColumn(numericCols, labels), rows, query, labels)
		}
		if len(numericCols) > 0 && len(categoryCols) > 0 {
			categoryCol := preferredCategoryColumn(categoryCols, labels)
			if isGroupedTimeSeries(rows, timeCol, categoryCol) {
				return groupedLineChart(timeCol, categoryCol, preferredNumericColumn(numericCols, labels), rows, query, labels)
			}
		}
		return nil
	}
	if len(categoryCols) >= 1 && len(numericCols) > 0 {
		if len(rows) <= 10 && containsAny(query, []string{"占比", "比例", "分布", "percent"}) {
			return pieChart(preferredCategoryColumn(categoryCols, labels), preferredNumericColumn(numericCols, labels), rows, query, labels)
		}
		return barChart(preferredCategoryColumn(categoryCols, labels), preferredNumericColumn(numericCols, labels), rows, query, labels)
	}
	if len(numericCols) > 0 {
		return barChart(columns[0], numericCols[0], rows, query, labels)
	}
	return nil
}

func isInternalColumn(column string) bool {
	name := strings.ToLower(strings.TrimSpace(column))
	return name == "_owner_id" || name == "owner_id" || name == "_row_number" || name == "row_number"
}

func isAggregatedTimeSeries(rows []map[string]interface{}, timeColumn string) bool {
	if len(rows) < 2 {
		return false
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		seen[fmt.Sprint(row[timeColumn])] = struct{}{}
	}
	return len(seen) >= 2 && len(seen) == len(rows)
}

func isGroupedTimeSeries(rows []map[string]interface{}, timeColumn, categoryColumn string) bool {
	if len(rows) < 4 || timeColumn == categoryColumn {
		return false
	}
	dates := map[string]struct{}{}
	categories := map[string]struct{}{}
	pairs := map[string]struct{}{}
	for _, row := range rows {
		date := fmt.Sprint(row[timeColumn])
		category := fmt.Sprint(row[categoryColumn])
		if date == "" || category == "" {
			return false
		}
		pair := date + "\x00" + category
		if _, exists := pairs[pair]; exists {
			return false
		}
		pairs[pair] = struct{}{}
		dates[date] = struct{}{}
		categories[category] = struct{}{}
	}
	return len(dates) >= 2 && len(categories) >= 2 && len(categories) <= 8
}

func preferredNumericColumn(columns []string, labels map[string]string) string {
	return preferredColumn(columns, labels, []string{"销售", "收入", "金额", "gmv", "revenue", "amount", "毛利", "利润", "订单量", "数量", "count"}, []string{"id", "编号", "小时", "点击", "展示", "会话"})
}

func preferredCategoryColumn(columns []string, labels map[string]string) string {
	return preferredColumn(columns, labels, []string{"品类", "商品", "渠道", "平台", "国家", "地区", "活动", "日期", "月"}, []string{"id", "编号", "订单号", "客户"})
}

func preferredColumn(columns []string, labels map[string]string, preferred, avoided []string) string {
	best, bestScore := columns[0], -100
	for _, column := range columns {
		text := strings.ToLower(column + " " + labels[column])
		score := 0
		for _, keyword := range preferred {
			if strings.Contains(text, strings.ToLower(keyword)) {
				score += 10
			}
		}
		for _, keyword := range avoided {
			if strings.Contains(text, strings.ToLower(keyword)) {
				score -= 20
			}
		}
		if score > bestScore {
			best, bestScore = column, score
		}
	}
	return best
}

func inferType(rows []map[string]interface{}, col string) string {
	for i := 0; i < len(rows) && i < 20; i++ {
		v := rows[i][col]
		if v == nil {
			continue
		}
		switch v.(type) {
		case int, int32, int64, float32, float64:
			return "number"
		default:
			s := fmt.Sprint(v)
			if (strings.Contains(s, "-") || strings.Contains(s, "/")) && len(s) >= 8 {
				return "date"
			}
			return "string"
		}
	}
	return "string"
}

func lineChart(xCol, yCol string, rows []map[string]interface{}, title string, labels map[string]string) map[string]interface{} {
	xData := make([]string, len(rows))
	yData := make([]float64, len(rows))
	for i, r := range rows {
		xData[i] = fmt.Sprint(r[xCol])
		yData[i] = toNum(r[yCol])
	}
	return map[string]interface{}{
		"title":   map[string]interface{}{"text": title, "left": "center"},
		"tooltip": map[string]interface{}{"trigger": "axis"},
		"xAxis":   map[string]interface{}{"type": "category", "name": columnLabel(labels, xCol), "data": xData},
		"yAxis":   map[string]interface{}{"type": "value", "name": columnLabel(labels, yCol)},
		"series":  []map[string]interface{}{{"name": columnLabel(labels, yCol), "type": "line", "data": yData, "smooth": true}},
	}
}

func groupedLineChart(timeCol, categoryCol, valueCol string, rows []map[string]interface{}, title string, labels map[string]string) map[string]interface{} {
	dates := map[string]struct{}{}
	categoryValues := map[string]map[string]float64{}
	for _, row := range rows {
		date := fmt.Sprint(row[timeCol])
		category := fmt.Sprint(row[categoryCol])
		dates[date] = struct{}{}
		if categoryValues[category] == nil {
			categoryValues[category] = map[string]float64{}
		}
		categoryValues[category][date] = toNum(row[valueCol])
	}
	xData := make([]string, 0, len(dates))
	for date := range dates {
		xData = append(xData, date)
	}
	sort.Strings(xData)
	categoryNames := make([]string, 0, len(categoryValues))
	for category := range categoryValues {
		categoryNames = append(categoryNames, category)
	}
	sort.Strings(categoryNames)
	series := make([]map[string]interface{}, 0, len(categoryNames))
	for _, category := range categoryNames {
		values := make([]float64, len(xData))
		for index, date := range xData {
			values[index] = categoryValues[category][date]
		}
		series = append(series, map[string]interface{}{"name": category, "type": "line", "data": values, "smooth": true})
	}
	return map[string]interface{}{
		"title":   map[string]interface{}{"text": title, "left": "center"},
		"tooltip": map[string]interface{}{"trigger": "axis"},
		"legend":  map[string]interface{}{"bottom": 0},
		"xAxis":   map[string]interface{}{"type": "category", "name": columnLabel(labels, timeCol), "data": xData},
		"yAxis":   map[string]interface{}{"type": "value", "name": columnLabel(labels, valueCol)},
		"series":  series,
	}
}

func barChart(xCol, yCol string, rows []map[string]interface{}, title string, labels map[string]string) map[string]interface{} {
	limit := len(rows)
	if limit > 20 {
		limit = 20
	}
	xData := make([]string, limit)
	yData := make([]float64, limit)
	for i := 0; i < limit; i++ {
		xData[i] = fmt.Sprint(rows[i][xCol])
		yData[i] = toNum(rows[i][yCol])
	}
	return map[string]interface{}{
		"title":   map[string]interface{}{"text": title, "left": "center"},
		"tooltip": map[string]interface{}{"trigger": "axis"},
		"xAxis":   map[string]interface{}{"type": "category", "name": columnLabel(labels, xCol), "data": xData},
		"yAxis":   map[string]interface{}{"type": "value", "name": columnLabel(labels, yCol)},
		"series":  []map[string]interface{}{{"name": columnLabel(labels, yCol), "type": "bar", "data": yData}},
	}
}

func pieChart(nameCol, valueCol string, rows []map[string]interface{}, title string, labels map[string]string) map[string]interface{} {
	limit := len(rows)
	if limit > 10 {
		limit = 10
	}
	data := make([]map[string]interface{}, limit)
	for i := 0; i < limit; i++ {
		data[i] = map[string]interface{}{"name": fmt.Sprint(rows[i][nameCol]), "value": toNum(rows[i][valueCol])}
	}
	return map[string]interface{}{
		"title":   map[string]interface{}{"text": title, "left": "center"},
		"tooltip": map[string]interface{}{"trigger": "item"},
		"legend":  map[string]interface{}{"bottom": 0},
		"series":  []map[string]interface{}{{"name": columnLabel(labels, valueCol), "type": "pie", "radius": "50%", "data": data}},
	}
}

func columnLabel(labels map[string]string, column string) string {
	if label := labels[column]; label != "" {
		return label
	}
	return column
}

func toNum(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f
	}
}

func containsAny(s string, keys []string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
