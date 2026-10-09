package chart

import "testing"

func TestBuildChartConfigSkipsRawDetailRowsWithRepeatedDates(t *testing.T) {
	rows := []map[string]interface{}{
		{"下单日期": "2026-04-01", "广告点击量": 1, "销售额": 30.0},
		{"下单日期": "2026-04-01", "广告点击量": 2, "销售额": 40.0},
	}
	if config := BuildChartConfig(rows, "查看订单明细", nil); config != nil {
		t.Fatalf("raw detail rows should not render a time series: %#v", config)
	}
}

func TestBuildChartConfigPrefersBusinessMetricAndHidesInternalColumns(t *testing.T) {
	rows := []map[string]interface{}{
		{"_owner_id": "secret", "下单日期": "2026-04-01", "广告点击量": 1, "销售额": 30.0},
		{"_owner_id": "secret", "下单日期": "2026-04-02", "广告点击量": 5, "销售额": 80.0},
	}
	config := BuildChartConfig(rows, "销售趋势", map[string]string{"销售额": "销售额"})
	if config == nil {
		t.Fatal("expected an aggregated series chart")
	}
	series := config["series"].([]map[string]interface{})
	if series[0]["name"] != "销售额" {
		t.Fatalf("expected sales metric, got %v", series[0]["name"])
	}
}

func TestBuildChartConfigBuildsGroupedTimeSeries(t *testing.T) {
	rows := []map[string]interface{}{
		{"日期": "2026-04-01", "商品品类": "家居收纳", "销售额": 30.0},
		{"日期": "2026-04-01", "商品品类": "护肤美容", "销售额": 50.0},
		{"日期": "2026-04-02", "商品品类": "家居收纳", "销售额": 80.0},
		{"日期": "2026-04-02", "商品品类": "护肤美容", "销售额": 40.0},
	}
	config := BuildChartConfig(rows, "各商品品类销售趋势", nil)
	if config == nil {
		t.Fatal("expected grouped time series chart")
	}
	series := config["series"].([]map[string]interface{})
	if len(series) != 2 {
		t.Fatalf("expected two category series, got %d", len(series))
	}
}
