package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jeffypooo/webtop/internal/metrics"
	"github.com/jeffypooo/webtop/internal/web"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
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
	w.Header().Set("Content-Type", "text/html")
	// Read interval from query parameter, default to "1s" if not provided
	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "1s"
	}
	web.Index(interval).Render(r.Context(), w)
}

func getMetrics() (metrics.Metrics, error) {
	cpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		return metrics.Metrics{}, fmt.Errorf("error getting CPU usage: %w", err)
	}
	memUsage, err := mem.VirtualMemory()
	if err != nil {
		return metrics.Metrics{}, fmt.Errorf("error getting memory usage: %w", err)
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
	}, nil
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

	// Send initial metrics
	fmt.Println("Sending initial metrics")
	sendMetricsUpdate(ctx, w, flusher)

	// Stream metrics at the specified interval
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			return
		case <-ticker.C:
			fmt.Println("Sending metrics update")
			sendMetricsUpdate(ctx, w, flusher)
		}
	}
}

func sendMetricsUpdate(ctx context.Context, w io.Writer, flusher http.Flusher) {
	m, err := getMetrics()
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	// Render metrics HTML
	var buf strings.Builder
	web.MetricsDisplay(m).Render(ctx, &buf)
	htmlContent := buf.String()
	fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", htmlContent)

	flusher.Flush()
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
