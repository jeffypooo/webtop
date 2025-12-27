package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"

	"github.com/jeffypooo/webtop/internal/metrics"
	"github.com/jeffypooo/webtop/internal/web"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
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
	intervalParam := c.QueryParam("interval")
	if intervalParam == "" {
		intervalParam = "1s"
	}
	limitParam := c.QueryParam("limit")
	if limitParam == "" {
		limitParam = "10"
	}

	web.Index(intervalParam, limitParam).Render(c.Request().Context(), c.Response().Writer)
	return nil
}

func apiMetricsSSEHandler(c echo.Context) error {
	c.Logger().Info("SSE request received", "remote_addr", c.Request().RemoteAddr, "method", c.Request().Method, "url", c.Request().URL.String())

	interval, err := parseInterval(c)
	if err != nil {
		return c.String(http.StatusBadRequest, fmt.Sprintf("Invalid interval: %v", err))
	}

	limit, err := strconv.Atoi(c.QueryParam("limit"))
	if err != nil {
		limit = 10
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
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Handle client disconnect
	ctx := c.Request().Context()

	procCache := make(map[int32]*process.Process)
	var lastNetStats *net.IOCountersStat
	var lastNetTime time.Time

	// Send initial metrics
	// fmt.Println("Sending initial metrics")
	c.Logger().Info("Sending initial metrics")
	lastNetStats, lastNetTime, err = sendMetricsUpdate(ctx, resp, procCache, lastNetStats, lastNetTime, limit)
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
			lastNetStats, lastNetTime, err = sendMetricsUpdate(ctx, resp, procCache, lastNetStats, lastNetTime, limit)
			if err != nil {
				c.Logger().Error("Error sending metrics update", "error", err)
				return c.String(http.StatusInternalServerError, fmt.Sprintf("Error sending metrics update: %v", err))
			}
		}
	}
}

func sendMetricsUpdate(ctx context.Context, resp *echo.Response, procCache map[int32]*process.Process, lastNetStats *net.IOCountersStat, lastNetTime time.Time, limit int) (*net.IOCountersStat, time.Time, error) {
	m, newNetStats, newNetTime, err := getMetrics(procCache, lastNetStats, lastNetTime, limit)
	if err != nil {
		_, writeErr := fmt.Fprintf(resp.Writer, "event: error\ndata: %s\n\n", err.Error())
		if writeErr != nil {
			return lastNetStats, lastNetTime, writeErr
		}
		resp.Flush()
		return lastNetStats, lastNetTime, nil
	}

	// Render metrics HTML
	var buf strings.Builder
	web.MetricsDisplay(m).Render(ctx, &buf)

	if ctx.Err() != nil {
		return lastNetStats, lastNetTime, ctx.Err()
	}

	htmlContent := strings.ReplaceAll(buf.String(), "\n", " ")
	_, writeErr := fmt.Fprintf(resp.Writer, "event: metrics\ndata: %s\n\n", htmlContent)
	if writeErr != nil {
		return newNetStats, newNetTime, writeErr
	}

	resp.Flush()
	return newNetStats, newNetTime, nil
}

func parseInterval(c echo.Context) (time.Duration, error) {
	intervalStr := c.QueryParam("interval")
	if intervalStr == "" {
		return time.Duration(1 * time.Second), nil
	}
	// Remove "ms" suffix and convert to milliseconds
	if strings.HasSuffix(intervalStr, "ms") {
		msStr := strings.TrimSuffix(intervalStr, "ms")
		ms, err := strconv.Atoi(msStr)
		if err != nil {
			return 0, err
		}
		return time.Duration(ms) * time.Millisecond, nil
	}

	// Remove "s" suffix and convert to seconds
	if strings.HasSuffix(intervalStr, "s") {
		sStr := strings.TrimSuffix(intervalStr, "s")
		seconds, err := strconv.ParseFloat(sStr, 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}

	// Default to seconds if no suffix
	seconds, err := strconv.ParseFloat(intervalStr, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func getMetrics(procCache map[int32]*process.Process, lastNetStats *net.IOCountersStat, lastNetTime time.Time, limit int) (metrics.Metrics, *net.IOCountersStat, time.Time, error) {
	cpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		return metrics.Metrics{}, nil, time.Time{}, fmt.Errorf("error getting CPU usage: %w", err)
	}
	memUsage, err := mem.VirtualMemory()
	if err != nil {
		return metrics.Metrics{}, nil, time.Time{}, fmt.Errorf("error getting memory usage: %w", err)
	}

	// Disk usage
	diskUsage, err := disk.Usage("/")
	if err != nil {
		return metrics.Metrics{}, nil, time.Time{}, fmt.Errorf("error getting disk usage: %w", err)
	}

	// Network usage
	netStats, err := net.IOCounters(false) // false = aggregated
	if err != nil {
		return metrics.Metrics{}, nil, time.Time{}, fmt.Errorf("error getting network usage: %w", err)
	}
	currentNetStats := &netStats[0]
	currentTime := time.Now()

	var txRate, rxRate uint64
	if lastNetStats != nil {
		duration := currentTime.Sub(lastNetTime).Seconds()
		if duration > 0 {
			txRate = uint64(float64(currentNetStats.BytesSent-lastNetStats.BytesSent) / duration)
			rxRate = uint64(float64(currentNetStats.BytesRecv-lastNetStats.BytesRecv) / duration)
		}
	}

	// Process metrics
	procs, err := process.Processes()
	if err != nil {
		return metrics.Metrics{}, nil, time.Time{}, fmt.Errorf("error getting processes: %w", err)
	}

	currentPids := make(map[int32]bool)
	for _, p := range procs {
		currentPids[p.Pid] = true
		if _, ok := procCache[p.Pid]; !ok {
			procCache[p.Pid] = p
		}
	}

	// Cleanup dead processes
	for pid := range procCache {
		if !currentPids[pid] {
			delete(procCache, pid)
		}
	}

	var processes []metrics.Process
	for _, p := range procCache {
		// Use Percent(0) which calculates based on last call time
		cpuPct, err := p.Percent(0)
		if err != nil {
			// Process might have died
			continue
		}

		name, err := p.Name()
		if err != nil {
			name = "unknown"
		}

		memInfo, err := p.MemoryInfo()
		var rss uint64
		if err == nil {
			rss = memInfo.RSS
		}

		processes = append(processes, metrics.Process{
			Pid:      p.Pid,
			Name:     name,
			CpuPct:   cpuPct,
			MemBytes: rss,
		})
	}

	// Sort by CPU
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].CpuPct > processes[j].CpuPct
	})

	// Top N
	if len(processes) > limit {
		processes = processes[:limit]
	}

	return metrics.Metrics{
		CpuUsage: metrics.CpuUsage{
			UsagePct: cpuUsage[0],
		},
		MemUsage: metrics.MemUsage{
			Used:     memUsage.Used,
			Free:     memUsage.Free,
			Total:    memUsage.Total,
			UsagePct: float64(memUsage.Used) / float64(memUsage.Total) * 100,
		},
		NetUsage: metrics.NetUsage{
			BytesSent: currentNetStats.BytesSent,
			BytesRecv: currentNetStats.BytesRecv,
			TxRate:    txRate,
			RxRate:    rxRate,
		},
		DiskUsage: metrics.DiskUsage{
			Path:        diskUsage.Path,
			Total:       diskUsage.Total,
			Free:        diskUsage.Free,
			Used:        diskUsage.Used,
			UsedPercent: diskUsage.UsedPercent,
		},
		Processes: processes,
	}, currentNetStats, currentTime, nil
}
