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

type Process struct {
	Pid      int32   `json:"pid"`
	Name     string  `json:"name"`
	CpuPct   float64 `json:"cpu_pct"`
	MemBytes uint64  `json:"mem_bytes"`
}

type Metrics struct {
	CpuUsage  CpuUsage  `json:"cpu"`
	MemUsage  MemUsage  `json:"mem"`
	Processes []Process `json:"processes"`
}
