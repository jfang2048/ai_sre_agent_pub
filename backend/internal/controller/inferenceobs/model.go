// Package inferenceobs projects pushed serving and GPU evidence into bounded,
// timestamp-aware monitoring state. It never invokes inference or remediation.
package inferenceobs

import "time"

type Config struct {
	StaleAfter              time.Duration `yaml:"stale_after"`
	Retention               time.Duration `yaml:"retention"`
	MaxEndpoints            int           `yaml:"max_endpoints"`
	MaxModelsPerEndpoint    int           `yaml:"max_models_per_endpoint"`
	MaxGPUs                 int           `yaml:"max_gpus"`
	WaitingWarning          float64       `yaml:"waiting_warning"`
	KVCacheWarningPercent   float64       `yaml:"kv_cache_warning_percent"`
	TTFTWarningSeconds      float64       `yaml:"ttft_warning_seconds"`
	ITLWarningSeconds       float64       `yaml:"itl_warning_seconds"`
	GPUMemoryWarningPercent float64       `yaml:"gpu_memory_warning_percent"`
}

func DefaultConfig() Config {
	return Config{StaleAfter: 2 * time.Minute, Retention: 30 * time.Minute, MaxEndpoints: 256,
		MaxModelsPerEndpoint: 32, MaxGPUs: 2048, WaitingWarning: 10, KVCacheWarningPercent: 90,
		TTFTWarningSeconds: 2, ITLWarningSeconds: .1, GPUMemoryWarningPercent: 95}
}

type Measurement struct {
	Value         float64   `json:"value"`
	Unit          string    `json:"unit"`
	ObservedAt    time.Time `json:"observed_at"`
	WindowSeconds float64   `json:"window_seconds,omitempty"`
}

type Finding struct {
	Code           string   `json:"code"`
	Severity       string   `json:"severity"`
	Summary        string   `json:"summary"`
	Recommendation string   `json:"recommendation"`
	Evidence       []string `json:"evidence"`
}

type Model struct {
	Name       string                 `json:"name"`
	Engine     string                 `json:"engine,omitempty"`
	ObservedAt time.Time              `json:"observed_at"`
	Status     string                 `json:"status"`
	Metrics    map[string]Measurement `json:"metrics"`
	Findings   []Finding              `json:"findings"`
}

type Endpoint struct {
	CollectorID   string     `json:"collector_id"`
	Hostname      string     `json:"hostname"`
	Endpoint      string     `json:"endpoint"`
	Status        string     `json:"status"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	Models        []Model    `json:"models"`
}

type GPU struct {
	CollectorID string                 `json:"collector_id"`
	Hostname    string                 `json:"hostname"`
	GPUID       string                 `json:"gpu_id"`
	ObservedAt  time.Time              `json:"observed_at"`
	Status      string                 `json:"status"`
	Metrics     map[string]Measurement `json:"metrics"`
	Findings    []Finding              `json:"findings"`
}

type Report struct {
	GeneratedAt       time.Time  `json:"generated_at"`
	StaleAfterSeconds float64    `json:"stale_after_seconds"`
	CapacityLimited   bool       `json:"capacity_limited"`
	Endpoints         []Endpoint `json:"endpoints"`
	GPUs              []GPU      `json:"gpus"`
}
