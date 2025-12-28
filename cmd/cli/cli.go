package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jeffypooo/webtop/internal/metrics"
	"github.com/labstack/gommon/log"
)

func main() {
	mc := metrics.NewMetricsCollector()
	metrics, err := mc.GetMetrics(metrics.MetricsParams{
		Interval:          1 * time.Second,
		ProcLimit:         10,
		ProcSort:          metrics.ProcSortCpu,
		ProcSortDirection: metrics.SortDirectionDesc,
	})
	if err != nil {
		log.Fatalf("Error getting metrics: %v", err)
	}
	// dump metrics to JSON
	json, err := json.MarshalIndent(metrics, "", " ")
	if err != nil {
		log.Fatalf("Error marshalling metrics: %v", err)
	}
	fmt.Println(string(json))
}
