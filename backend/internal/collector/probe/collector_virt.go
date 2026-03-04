// Package probe implements virtualization stats collection.
// Uses libvirt (via virsh CLI to avoid CGO/dependency issues) to monitor hypervisor stats.
package probe

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// VirtCollector collects virtualization metrics
type VirtCollector struct {
	enabled bool
}

// NewVirtCollector creates a new virtualization collector
func NewVirtCollector() *VirtCollector {
	// Check if we are running on a hypervisor (can run virsh)
	_, err := exec.LookPath("virsh")
	return &VirtCollector{
		enabled: err == nil,
	}
}

// Collect gathers virtualization metrics via virsh domstats
func (vc *VirtCollector) Collect(now time.Time) ([]Metric, error) {
	if !vc.enabled {
		return nil, nil
	}

	// Run virsh domstats --cpu-total --balloon --block --network
	cmd := exec.Command("virsh", "domstats", "--cpu-total", "--balloon", "--block", "--network")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return vc.parseDomStats(out.String(), now)
}

func (vc *VirtCollector) parseDomStats(output string, now time.Time) ([]Metric, error) {
	var metrics []Metric
	scanner := bufio.NewScanner(strings.NewReader(output))

	var currentDomain string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "Domain: ") {
			currentDomain = strings.TrimPrefix(line, "Domain: ")
			currentDomain = strings.Trim(currentDomain, "'")
			continue
		}

		if currentDomain == "" {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		valueStr := parts[1]

		// Parse metrics we care about
		if strings.HasSuffix(key, ".time") {
			// CPU time (ns)
			val, err := strconv.ParseFloat(valueStr, 64)
			if err == nil {
				metrics = append(metrics, Metric{
					Name:      "libvirt_domain_cpu_seconds_total",
					Type:      "counter",
					Value:     val / 1e9,
					Labels:    map[string]string{"domain": currentDomain},
					Timestamp: now,
				})
			}
		} else if strings.HasSuffix(key, ".current") && strings.Contains(key, "balloon") {
			// Memory current (KiB)
			val, err := strconv.ParseFloat(valueStr, 64)
			if err == nil {
				metrics = append(metrics, Metric{
					Name:      "libvirt_domain_memory_usage_bytes",
					Type:      "gauge",
					Value:     val * 1024,
					Labels:    map[string]string{"domain": currentDomain},
					Timestamp: now,
				})
			}
		} else if strings.HasSuffix(key, ".rx.bytes") {
			// Network RX
			val, err := strconv.ParseFloat(valueStr, 64)
			if err == nil {
				// key format is usually net.<index>.rx.bytes; preserve index as label.
				metrics = append(metrics, Metric{
					Name:      "libvirt_domain_net_rx_bytes_total",
					Type:      "counter",
					Value:     val,
					Labels:    map[string]string{"domain": currentDomain, "interface": extractIndex(key)},
					Timestamp: now,
				})
			}
		} else if strings.HasSuffix(key, ".tx.bytes") {
			// Network TX
			val, err := strconv.ParseFloat(valueStr, 64)
			if err == nil {
				metrics = append(metrics, Metric{
					Name:      "libvirt_domain_net_tx_bytes_total",
					Type:      "counter",
					Value:     val,
					Labels:    map[string]string{"domain": currentDomain, "interface": extractIndex(key)},
					Timestamp: now,
				})
			}
		}
	}

	return metrics, nil
}

func extractIndex(key string) string {
	parts := strings.Split(key, ".")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "unknown"
}
