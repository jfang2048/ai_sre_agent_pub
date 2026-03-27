package probe

import (
	"os"
	"regexp"
	"strings"
)

type processContext struct {
	WorkloadClass string
	Job           string
	CommPattern   string
	PodUID        string
}

var (
	podUIDPatternHyphenated = regexp.MustCompile(`pod([0-9a-fA-F]{8}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{12})`)
	podUIDPatternCompact    = regexp.MustCompile(`pod([0-9a-fA-F]{32})`)
)

func buildProcessContext(pid int, fallbackName string) processContext {
	cmdline := readProcessCmdline(pid)
	searchText := strings.ToLower(strings.TrimSpace(cmdline))
	if searchText == "" {
		searchText = strings.ToLower(strings.TrimSpace(fallbackName))
	}

	job := extractJobHint(cmdline)
	commPattern := detectCommPattern(searchText)
	workloadClass := classifyWorkload(searchText)
	podUID := extractPodUID(readProcessCgroup(pid))

	return processContext{
		WorkloadClass: sanitizeContextValue(workloadClass),
		Job:           sanitizeContextValue(job),
		CommPattern:   sanitizeContextValue(commPattern),
		PodUID:        sanitizeContextValue(podUID),
	}
}

func applyProcessContextLabels(labels map[string]string, ctx processContext) map[string]string {
	if labels == nil {
		labels = make(map[string]string)
	}
	if ctx.WorkloadClass != "" {
		labels["workload_class"] = ctx.WorkloadClass
	}
	if ctx.Job != "" {
		labels["job"] = ctx.Job
	}
	if ctx.CommPattern != "" {
		labels["comm_pattern"] = ctx.CommPattern
	}
	if ctx.PodUID != "" {
		labels["pod_uid"] = ctx.PodUID
	}
	return labels
}

func readProcessCmdline(pid int) string {
	data, err := os.ReadFile("/proc/" + itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return ""
	}
	text := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.TrimSpace(text)
}

func readProcessCgroup(pid int) string {
	data, err := os.ReadFile("/proc/" + itoa(pid) + "/cgroup")
	if err != nil || len(data) == 0 {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func classifyWorkload(searchText string) string {
	if searchText == "" {
		return "unknown"
	}

	trainingHints := []string{
		"torchrun", "deepspeed", "horovod", "allreduce", "distributed", "trainer",
		"megatron", "accelerate launch", "nproc_per_node", "nnodes", "nccl",
	}
	for _, hint := range trainingHints {
		if strings.Contains(searchText, hint) {
			return "training"
		}
	}

	inferenceHints := []string{
		"tritonserver", "vllm", "text-generation", "inference", "model-server",
		"serve", "api-server", "llm", "tokenizer", "batcher",
	}
	for _, hint := range inferenceHints {
		if strings.Contains(searchText, hint) {
			return "inference"
		}
	}

	systemHints := []string{"kubelet", "containerd", "dockerd", "systemd", "sshd", "journald"}
	for _, hint := range systemHints {
		if strings.Contains(searchText, hint) {
			return "system"
		}
	}

	return "unknown"
}

func detectCommPattern(searchText string) string {
	if searchText == "" {
		return ""
	}
	if strings.Contains(searchText, "nccl") || strings.Contains(searchText, "allreduce") {
		return "nccl"
	}
	if strings.Contains(searchText, "ibverbs") || strings.Contains(searchText, "rdma") || strings.Contains(searchText, "mlx") {
		return "rdma"
	}
	if strings.Contains(searchText, "ucx") {
		return "ucx"
	}
	if strings.Contains(searchText, "mpi") || strings.Contains(searchText, "mpirun") {
		return "mpi"
	}
	if strings.Contains(searchText, "gloo") {
		return "gloo"
	}
	return ""
}

func extractJobHint(cmdline string) string {
	if strings.TrimSpace(cmdline) == "" {
		return ""
	}
	tokens := strings.Fields(cmdline)
	flagKeys := []string{"--job", "--job-name", "--run_name", "--run-name", "--experiment", "--exp", "--task", "--model", "--pipeline-name"}
	for _, token := range tokens {
		lower := strings.ToLower(token)
		for _, flag := range flagKeys {
			if strings.HasPrefix(lower, flag+"=") {
				parts := strings.SplitN(token, "=", 2)
				if len(parts) == 2 {
					return parts[1]
				}
			}
		}
		if strings.HasPrefix(lower, "job=") || strings.HasPrefix(lower, "job_name=") || strings.HasPrefix(lower, "run_name=") {
			parts := strings.SplitN(token, "=", 2)
			if len(parts) == 2 {
				return parts[1]
			}
		}
	}

	for i := 0; i < len(tokens)-1; i++ {
		current := strings.ToLower(tokens[i])
		for _, flag := range flagKeys {
			if current == flag {
				return tokens[i+1]
			}
		}
	}

	return ""
}

func extractPodUID(cgroupText string) string {
	if cgroupText == "" {
		return ""
	}
	if match := podUIDPatternHyphenated.FindStringSubmatch(cgroupText); len(match) > 1 {
		return strings.ReplaceAll(strings.ToLower(match[1]), "_", "-")
	}
	if match := podUIDPatternCompact.FindStringSubmatch(cgroupText); len(match) > 1 {
		return strings.ToLower(match[1])
	}
	return ""
}

func sanitizeContextValue(in string) string {
	value := strings.TrimSpace(in)
	if value == "" {
		return ""
	}
	if len(value) > 96 {
		value = value[:96]
	}
	builder := strings.Builder{}
	builder.Grow(len(value))
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
			builder.WriteRune(ch)
		case ch == '-', ch == '_', ch == '.', ch == ':', ch == '/':
			builder.WriteRune(ch)
		case ch == ' ':
			builder.WriteRune('_')
		default:
			// drop unsupported label rune
		}
	}
	return strings.Trim(builder.String(), "_./:-")
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
