package metrics

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
