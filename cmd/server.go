package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"

	"github.com/jeffypooo/webtop/internal/metrics"
	"github.com/jeffypooo/webtop/internal/web"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

func main() {
	e := echo.New()
	e.Logger.SetLevel(log.DEBUG)
	e.GET("/", rootHandler)
	e.GET("/api/metrics/sse", apiMetricsSSEHandler)
	e.Logger.Fatal(e.Start(":8080"))

}

func rootHandler(c echo.Context) error {
	params, err := parseMetricsParams(c)
	if err != nil {
		return c.String(http.StatusBadRequest, fmt.Sprintf("Invalid interval: %v", err))
	}
	web.Index(params).Render(c.Request().Context(), c.Response().Writer)
	return nil
}

func apiMetricsSSEHandler(c echo.Context) error {
	c.Logger().Info("SSE request received", "remote_addr", c.Request().RemoteAddr, "method", c.Request().Method, "url", c.Request().URL.String())

	params, err := parseMetricsParams(c)
	if err != nil {
		return c.String(http.StatusBadRequest, fmt.Sprintf("Invalid interval: %v", err))
	}

	// Set headers for SSE
	resp := c.Response()
	resp.Header().Set("Content-Type", "text/event-stream")
	resp.Header().Set("Cache-Control", "no-cache")
	resp.Header().Set("Connection", "keep-alive")
	resp.Header().Set("Access-Control-Allow-Origin", "*")

	// Send initial connection message
	fmt.Fprintf(resp.Writer, "event: connected\ndata: Connected to metrics stream\n\n")
	resp.Flush()

	// Create a ticker for the interval
	ticker := time.NewTicker(params.Interval)
	defer ticker.Stop()

	// Handle client disconnect
	ctx := c.Request().Context()

	procCache := make(map[int32]*process.Process)
	var lastNetStats *net.IOCountersStat
	var lastNetTime time.Time

	// Send initial metrics
	// fmt.Println("Sending initial metrics")
	c.Logger().Info("Sending initial metrics")
	lastNetStats, lastNetTime, err = sendMetricsUpdate(ctx, resp, procCache, lastNetStats, lastNetTime, params)
	if err != nil {
		fmt.Printf("Error sending initial metrics: %v\n", err)
		return c.String(http.StatusInternalServerError, fmt.Sprintf("Error sending initial metrics: %v", err))
	}

	// Stream metrics at the specified interval
	for {
		select {
		case <-ctx.Done():
			c.Logger().Info("Client disconnected (context done)")
			return nil
		case <-ticker.C:
			if ctx.Err() != nil {
				c.Logger().Info("Client disconnected (context done in loop)")
				return nil
			}
			c.Logger().Info("Sending metrics update")
			lastNetStats, lastNetTime, err = sendMetricsUpdate(ctx, resp, procCache, lastNetStats, lastNetTime, params)
			if err != nil {
				c.Logger().Error("Error sending metrics update", "error", err)
				return c.String(http.StatusInternalServerError, fmt.Sprintf("Error sending metrics update: %v", err))
			}
		}
	}
}

func sendMetricsUpdate(
	ctx context.Context,
	resp *echo.Response,
	procCache map[int32]*process.Process,
	lastNetStats *net.IOCountersStat,
	lastNetTime time.Time,
	params metrics.MetricsParams,
) (*net.IOCountersStat, time.Time, error) {
	m, newNetStats, newNetTime, err := metrics.GetMetrics(procCache, lastNetStats, lastNetTime, params)
	if err != nil {
		_, writeErr := fmt.Fprintf(resp.Writer, "event: error\ndata: %s\n\n", err.Error())
		if writeErr != nil {
			return lastNetStats, lastNetTime, writeErr
		}
		resp.Flush()
		return lastNetStats, lastNetTime, nil
	}

	// Render metrics HTML
	var metricsBuf strings.Builder
	web.MetricsCards(m).Render(ctx, &metricsBuf)
	metricsHtml := strings.ReplaceAll(metricsBuf.String(), "\n", " ")
	_, writeErr := fmt.Fprintf(resp.Writer, "event: metrics\ndata: %s\n\n", metricsHtml)
	if writeErr != nil {
		return newNetStats, lastNetTime, writeErr
	}

	var processesBuf strings.Builder
	web.ProcessList(m.Processes, params).Render(ctx, &processesBuf)
	processesHtml := strings.ReplaceAll(processesBuf.String(), "\n", " ")
	_, writeErr = fmt.Fprintf(resp.Writer, "event: processes\ndata: %s\n\n", processesHtml)
	if writeErr != nil {
		return newNetStats, lastNetTime, writeErr
	}

	if ctx.Err() != nil {
		return lastNetStats, lastNetTime, ctx.Err()
	}

	resp.Flush()
	return newNetStats, newNetTime, nil
}

func parseMetricsParams(c echo.Context) (metrics.MetricsParams, error) {
	interval := c.QueryParam("interval")
	if interval == "" {
		interval = "1s"
	}
	intervalDuration, err := time.ParseDuration(interval)
	if err != nil {
		return metrics.MetricsParams{}, fmt.Errorf("invalid interval: %w", err)
	}
	limit, err := strconv.Atoi(c.QueryParam("limit"))
	if err != nil {
		limit = 10
	}
	procSort := metrics.ProcSort(c.QueryParam("proc_sort"))
	if procSort == "" {
		procSort = metrics.ProcSortMem
	}
	procSortDirection := metrics.SortDirection(c.QueryParam("proc_sort_direction"))
	if procSortDirection == "" {
		procSortDirection = metrics.SortDirectionDesc
	}
	return metrics.MetricsParams{
		Interval:          intervalDuration,
		ProcLimit:         limit,
		ProcSort:          procSort,
		ProcSortDirection: procSortDirection,
	}, nil
}
