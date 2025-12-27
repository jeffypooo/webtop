package metrics

import (
	"fmt"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
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

func GetMetrics(
	procCache map[int32]*process.Process,
	lastNetStats *net.IOCountersStat,
	lastNetTime time.Time,
	params MetricsParams,
) (Metrics, *net.IOCountersStat, time.Time, error) {
	cpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		return Metrics{}, nil, time.Time{}, fmt.Errorf("error getting CPU usage: %w", err)
	}
	memUsage, err := mem.VirtualMemory()
	if err != nil {
		return Metrics{}, nil, time.Time{}, fmt.Errorf("error getting memory usage: %w", err)
	}

	// Disk usage
	diskUsage, err := disk.Usage("/")
	if err != nil {
		return Metrics{}, nil, time.Time{}, fmt.Errorf("error getting disk usage: %w", err)
	}

	// Network usage
	netStats, err := net.IOCounters(false) // false = aggregated
	if err != nil {
		return Metrics{}, nil, time.Time{}, fmt.Errorf("error getting network usage: %w", err)
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
		return Metrics{}, nil, time.Time{}, fmt.Errorf("error getting processes: %w", err)
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

	var processes []Process
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
	}, currentNetStats, currentTime, nil
}
