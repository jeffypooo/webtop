package metrics

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/shirou/gopsutil/v4/sensors"
)

type SortDirection string

const (
	SortDirectionAsc  SortDirection = "asc"
	SortDirectionDesc SortDirection = "desc"
)

type ProcSort string

const (
	ProcSortCpu  ProcSort = "cpu"
	ProcSortMem  ProcSort = "mem"
	ProcSortPid  ProcSort = "pid"
	ProcSortName ProcSort = "name"
)

type MetricsParams struct {
	Interval          time.Duration
	ProcLimit         int
	ProcSort          ProcSort
	ProcSortDirection SortDirection
}

type CpuUsage struct {
	UsagePct float64 `json:"usage"`
}

type MemUsage struct {
	Used     uint64  `json:"used"`
	Free     uint64  `json:"free"`
	Total    uint64  `json:"total"`
	UsagePct float64 `json:"usage"`
}

type NetUsage struct {
	BytesSent uint64 `json:"bytes_sent"`
	BytesRecv uint64 `json:"bytes_recv"`
	TxRate    uint64 `json:"tx_rate"` // Bytes/sec
	RxRate    uint64 `json:"rx_rate"` // Bytes/sec
}

type DiskUsage struct {
	Path        string  `json:"path"`
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"used_percent"`
}

type Process struct {
	Pid      int32   `json:"pid"`
	Name     string  `json:"name"`
	CpuPct   float64 `json:"cpu_pct"`
	MemBytes uint64  `json:"mem_bytes"`
}

type Metrics struct {
	CpuUsage  CpuUsage  `json:"cpu"`
	MemUsage  MemUsage  `json:"mem"`
	NetUsage  NetUsage  `json:"net"`
	DiskUsage DiskUsage `json:"disk"`
	Processes []Process `json:"processes"`
}

type MetricsCollector struct {
	mu           sync.Mutex
	procCache    map[int32]*process.Process
	lastNetStats *net.IOCountersStat
	lastNetTime  time.Time
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		procCache:    make(map[int32]*process.Process),
		lastNetStats: nil,
		lastNetTime:  time.Time{},
	}
}

// GetMetrics gets the metrics for the system. It is thread-safe.
func (mc *MetricsCollector) GetMetrics(
	params MetricsParams,
) (Metrics, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	sensors, err := sensors.SensorsTemperatures()
	if err != nil {
		return Metrics{}, fmt.Errorf("error getting sensors temperatures: %w", err)
	}
	for _, sensor := range sensors {
		fmt.Printf("Sensor: %+v\n", sensor)
	}

	cpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		return Metrics{}, fmt.Errorf("error getting CPU usage: %w", err)
	}

	memUsage, err := mem.VirtualMemory()
	if err != nil {
		return Metrics{}, fmt.Errorf("error getting memory usage: %w", err)
	}

	// Disk usage
	diskUsage, err := disk.Usage("/")
	if err != nil {
		return Metrics{}, fmt.Errorf("error getting disk usage: %w", err)
	}

	// Network usage
	netStats, err := net.IOCounters(false) // false = aggregated
	if err != nil {
		return Metrics{}, fmt.Errorf("error getting network usage: %w", err)
	}

	currentNetStats := &netStats[0]
	currentTime := time.Now()

	var txRate, rxRate uint64
	if mc.lastNetStats != nil {
		duration := currentTime.Sub(mc.lastNetTime).Seconds()
		if duration > 0 {
			txRate = uint64(float64(currentNetStats.BytesSent-mc.lastNetStats.BytesSent) / duration)
			rxRate = uint64(float64(currentNetStats.BytesRecv-mc.lastNetStats.BytesRecv) / duration)
		}
	}

	// Process metrics
	procs, err := process.Processes()
	if err != nil {
		return Metrics{}, fmt.Errorf("error getting processes: %w", err)
	}

	currentPids := make(map[int32]bool)
	for _, p := range procs {
		currentPids[p.Pid] = true
		if _, ok := mc.procCache[p.Pid]; !ok {
			mc.procCache[p.Pid] = p
		}
	}

	// Cleanup dead processes
	for pid := range mc.procCache {
		if !currentPids[pid] {
			delete(mc.procCache, pid)
		}
	}

	var processes []Process
	for _, p := range mc.procCache {
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

		processes = append(processes, Process{
			Pid:      p.Pid,
			Name:     name,
			CpuPct:   cpuPct,
			MemBytes: rss,
		})
	}

	// Sort by procSort and procSortDirection
	sort.Slice(processes, func(i, j int) bool {
		switch params.ProcSort {
		case ProcSortCpu:
			if params.ProcSortDirection == SortDirectionAsc {
				return processes[i].CpuPct < processes[j].CpuPct
			} else {
				return processes[i].CpuPct > processes[j].CpuPct
			}
		case ProcSortMem:
			if params.ProcSortDirection == SortDirectionAsc {
				return processes[i].MemBytes < processes[j].MemBytes
			} else {
				return processes[i].MemBytes > processes[j].MemBytes
			}
		case ProcSortPid:
			if params.ProcSortDirection == SortDirectionAsc {
				return processes[i].Pid < processes[j].Pid
			} else {
				return processes[i].Pid > processes[j].Pid
			}
		case ProcSortName:
			if params.ProcSortDirection == SortDirectionAsc {
				return processes[i].Name < processes[j].Name
			} else {
				return processes[i].Name > processes[j].Name
			}
		}
		return false
	})

	// Top N
	if len(processes) > params.ProcLimit {
		processes = processes[:params.ProcLimit]
	}

	mc.lastNetStats = currentNetStats
	mc.lastNetTime = currentTime

	return Metrics{
		CpuUsage: CpuUsage{
			UsagePct: cpuUsage[0],
		},
		MemUsage: MemUsage{
			Used:     memUsage.Used,
			Free:     memUsage.Free,
			Total:    memUsage.Total,
			UsagePct: float64(memUsage.Used) / float64(memUsage.Total) * 100,
		},
		NetUsage: NetUsage{
			BytesSent: currentNetStats.BytesSent,
			BytesRecv: currentNetStats.BytesRecv,
			TxRate:    txRate,
			RxRate:    rxRate,
		},
		DiskUsage: DiskUsage{
			Path:        diskUsage.Path,
			Total:       diskUsage.Total,
			Free:        diskUsage.Free,
			Used:        diskUsage.Used,
			UsedPercent: diskUsage.UsedPercent,
		},
		Processes: processes,
	}, nil
}

// StreamMetrics streams the metrics for the system. It is thread-safe.
func (mc *MetricsCollector) StreamMetrics(ctx context.Context, params MetricsParams) chan Metrics {
	ch := make(chan Metrics)
	go func() {
		defer close(ch)

		initialMetrics, err := mc.GetMetrics(params)
		if err != nil {
			fmt.Printf("Error getting initial metrics: %v\n", err)
			return
		}
		select {
		case ch <- initialMetrics:
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(params.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				metrics, err := mc.GetMetrics(params)
				if err != nil {
					fmt.Printf("Error getting metrics: %v\n", err)
					continue
				}
				select {
				case ch <- metrics:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch
}
