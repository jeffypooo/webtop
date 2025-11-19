package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jeffypooo/webtop/internal/metrics"
	"github.com/jeffypooo/webtop/internal/web"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

func main() {
	// TODO: implement real server

	// for now, just spin up a hello world server
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/api/metrics/sse", apiMetricsSSEHandler)
	fmt.Println("Starting server on port 8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Error starting server:", err)
		os.Exit(1)
	}
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if !r.URL.Query().Has("interval") {
		limitParam := ""
		if r.URL.Query().Has("limit") {
			limitParam = "&limit=" + r.URL.Query().Get("limit")
		}
		http.Redirect(w, r, "/?interval=1s"+limitParam, http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	// Read interval from query parameter
	interval := r.URL.Query().Get("interval")
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "10"
	}
	web.Index(interval, limit).Render(r.Context(), w)
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

func apiMetricsSSEHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("SSE request received from %s: %s %s\n", r.RemoteAddr, r.Method, r.URL.String())
	// Parse interval from query parameter
	intervalStr := r.URL.Query().Get("interval")
	if intervalStr == "" {
		intervalStr = "1s"
	}

	interval, err := parseInterval(intervalStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid interval: %v", err), http.StatusBadRequest)
		return
	}

	// Parse limit from query parameter
	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		limitStr = "10"
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		limit = 10
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create a flusher to send data immediately
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send initial connection message
	fmt.Fprintf(w, "event: connected\ndata: Connected to metrics stream\n\n")
	flusher.Flush()

	// Create a ticker for the interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Handle client disconnect
	ctx := r.Context()

	procCache := make(map[int32]*process.Process)
	var lastNetStats *net.IOCountersStat
	var lastNetTime time.Time

	// Send initial metrics
	fmt.Println("Sending initial metrics")
	lastNetStats, lastNetTime, err = sendMetricsUpdate(ctx, w, flusher, procCache, lastNetStats, lastNetTime, limit)
	if err != nil {
		fmt.Printf("Error sending initial metrics: %v\n", err)
		return
	}

	// Stream metrics at the specified interval
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Client disconnected (context done)")
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				fmt.Println("Client disconnected (context done in loop)")
				return
			}
			fmt.Println("Sending metrics update")
			lastNetStats, lastNetTime, err = sendMetricsUpdate(ctx, w, flusher, procCache, lastNetStats, lastNetTime, limit)
			if err != nil {
				fmt.Printf("Error sending metrics update: %v\n", err)
				return
			}
		}
	}
}

func sendMetricsUpdate(ctx context.Context, w io.Writer, flusher http.Flusher, procCache map[int32]*process.Process, lastNetStats *net.IOCountersStat, lastNetTime time.Time, limit int) (*net.IOCountersStat, time.Time, error) {
	m, newNetStats, newNetTime, err := getMetrics(procCache, lastNetStats, lastNetTime, limit)
	if err != nil {
		_, writeErr := fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		if writeErr != nil {
			return lastNetStats, lastNetTime, writeErr
		}
		flusher.Flush()
		return lastNetStats, lastNetTime, nil
	}

	// Render metrics HTML
	var buf strings.Builder
	web.MetricsDisplay(m).Render(ctx, &buf)

	if ctx.Err() != nil {
		return lastNetStats, lastNetTime, ctx.Err()
	}

	htmlContent := strings.ReplaceAll(buf.String(), "\n", " ")
	_, writeErr := fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", htmlContent)
	if writeErr != nil {
		return newNetStats, newNetTime, writeErr
	}

	flusher.Flush()
	return newNetStats, newNetTime, nil
}

func parseInterval(intervalStr string) (time.Duration, error) {
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
