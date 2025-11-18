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

type Metrics struct {
	CpuUsage CpuUsage `json:"cpu"`
	MemUsage MemUsage `json:"mem"`
}

