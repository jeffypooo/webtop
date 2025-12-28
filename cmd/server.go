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

	// Handle client disconnect
	ctx := c.Request().Context()

	mc := metrics.NewMetricsCollector()
	metricsCh := mc.StreamMetrics(ctx, params)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case metrics := <-metricsCh:
			err = sendMetricsUpdate(ctx, resp, params, metrics)
			if err != nil {
				return err
			}
		}
	}
}

func sendMetricsUpdate(
	ctx context.Context,
	resp *echo.Response,
	params metrics.MetricsParams,
	metrics metrics.Metrics,
) error {
	// Render metrics HTML
	var metricsBuf strings.Builder
	web.MetricsCards(metrics).Render(ctx, &metricsBuf)
	metricsHtml := strings.ReplaceAll(metricsBuf.String(), "\n", " ")
	_, writeErr := fmt.Fprintf(resp.Writer, "event: metrics\ndata: %s\n\n", metricsHtml)
	if writeErr != nil {
		return writeErr
	}

	var processesBuf strings.Builder
	web.ProcessList(metrics.Processes, params).Render(ctx, &processesBuf)
	processesHtml := strings.ReplaceAll(processesBuf.String(), "\n", " ")
	_, writeErr = fmt.Fprintf(resp.Writer, "event: processes\ndata: %s\n\n", processesHtml)
	if writeErr != nil {
		return writeErr
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	resp.Flush()
	return nil
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
