package probe

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	securityMaxWalkEntries   = 6000
	securityFileSizeLimitB   = int64(100 * 1024 * 1024)
	securityStalePortAge     = time.Hour
	securityDefaultAuditSpan = 5 * time.Minute
)

func (c *Collector) collectSecurityMetrics(now time.Time) []Metric {
	if c.securityAuditInterval <= 0 {
		c.securityAuditInterval = securityDefaultAuditSpan
	}
	if !c.lastSecurityCollect.IsZero() && now.Sub(c.lastSecurityCollect) < c.securityAuditInterval && len(c.securityMetricsCache) > 0 {
		cached := make([]Metric, 0, len(c.securityMetricsCache))
		for _, metric := range c.securityMetricsCache {
			copied := metric
			copied.Timestamp = now
			cached = append(cached, copied)
		}
		return cached
	}

	metrics := make([]Metric, 0, 24)
	appendMetric := func(name string, value float64) {
		metrics = append(metrics, Metric{Name: name, Type: "gauge", Value: value, Timestamp: now})
	}

	worldWritable, sensitiveReadable := scanSensitivePathPermissions()
	appendMetric("node_security_world_writable_sensitive_paths", float64(worldWritable))
	appendMetric("node_security_sensitive_readable_files_count", float64(sensitiveReadable))
	appendMetric("node_security_weak_permission_count", float64(worldWritable+sensitiveReadable))

	suidSgid := scanSUIDSGIDBinaries()
	appendMetric("node_security_suid_sgid_binaries_count", float64(suidSgid))

	sshWeakPerm, sshInsecureCfg := scanSSHPosture()
	appendMetric("node_security_ssh_weak_permissions_count", float64(sshWeakPerm))
	appendMetric("node_security_ssh_insecure_config_count", float64(sshInsecureCfg))

	largeFileCount, growthBytes := c.scanLargeFileGrowth()
	appendMetric("node_security_large_files_count", float64(largeFileCount))
	appendMetric("node_security_large_file_growth_bytes", float64(growthBytes))

	listeningPorts, unexpectedPorts, stalePorts, suspiciousOutbound, synBacklogPressure := c.scanNetworkExposure(now)
	appendMetric("node_security_listening_ports_count", float64(listeningPorts))
	appendMetric("node_security_unexpected_listening_ports_count", float64(unexpectedPorts))
	appendMetric("node_security_stale_listening_ports_count", float64(stalePorts))
	appendMetric("node_security_suspicious_outbound_destinations_count", float64(suspiciousOutbound))
	appendMetric("node_security_syn_backlog_pressure_ratio", synBacklogPressure)

	sysctlRisky := scanSysctlRisky()
	appendMetric("node_security_sysctl_risky_count", float64(sysctlRisky))

	firewallDisabled := scanFirewallDisabled()
	appendMetric("node_security_firewall_disabled", float64(firewallDisabled))

	selinuxDisabled, apparmorDisabled := scanMACStatus()
	appendMetric("node_security_selinux_disabled", float64(selinuxDisabled))
	appendMetric("node_security_apparmor_disabled", float64(apparmorDisabled))

	privProcAnomalies := scanPrivilegedProcessPaths()
	appendMetric("node_security_privileged_unusual_path_process_count", float64(privProcAnomalies))

	cronAnomalies, systemdUnknown := scanSchedulerUnits()
	appendMetric("node_security_cron_anomalies_count", float64(cronAnomalies))
	appendMetric("node_security_systemd_unknown_units_count", float64(systemdUnknown))

	containerPrivileged, containerCapabilityRisk := scanContainerEscapeRisk()
	appendMetric("node_security_container_privileged_count", float64(containerPrivileged))
	appendMetric("node_security_container_capability_risk_count", float64(containerCapabilityRisk))

	appendMetric("node_security_package_vulnerability_count", packageVulnerabilityHint())

	c.lastSecurityCollect = now
	c.securityMetricsCache = metrics
	return metrics
}

func scanSensitivePathPermissions() (worldWritable int, sensitiveReadable int) {
	roots := []string{"/etc", "/opt", "/var/lib", "/home"}
	sensitiveNameParts := []string{".env", ".pem", ".key", "id_rsa", "backup", ".bak"}

	seen := 0
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if seen >= securityMaxWalkEntries {
				return filepath.SkipDir
			}
			seen++

			if d.IsDir() {
				if depthFromRoot(root, path) > 4 {
					return filepath.SkipDir
				}
				info, infoErr := d.Info()
				if infoErr == nil && info.Mode().Perm()&0o002 != 0 {
					worldWritable++
				}
				return nil
			}

			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			mode := info.Mode().Perm()
			if mode&0o002 != 0 {
				worldWritable++
			}
			name := strings.ToLower(filepath.Base(path))
			for _, part := range sensitiveNameParts {
				if strings.Contains(name, part) {
					if mode&0o004 != 0 || mode&0o040 != 0 {
						sensitiveReadable++
					}
					break
				}
			}
			return nil
		})
	}
	return worldWritable, sensitiveReadable
}

func scanSUIDSGIDBinaries() int {
	dirs := []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin"}
	count := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			mode := info.Mode()
			if mode&os.ModeSetuid != 0 || mode&os.ModeSetgid != 0 {
				count++
			}
		}
	}
	return count
}

func scanSSHPosture() (weakPerm int, insecureCfg int) {
	keyPatterns := []string{"/etc/ssh/ssh_host_rsa_key", "/etc/ssh/ssh_host_ecdsa_key", "/etc/ssh/ssh_host_ed25519_key"}
	for _, keyPath := range keyPatterns {
		info, err := os.Stat(keyPath)
		if err != nil {
			continue
		}
		if info.Mode().Perm() > 0o600 {
			weakPerm++
		}
	}

	data, err := os.ReadFile("/etc/ssh/sshd_config")
	if err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			clean := strings.TrimSpace(strings.ToLower(line))
			if clean == "" || strings.HasPrefix(clean, "#") {
				continue
			}
			if strings.HasPrefix(clean, "passwordauthentication") && strings.Contains(clean, "yes") {
				insecureCfg++
			}
			if strings.HasPrefix(clean, "permitrootlogin") && !strings.Contains(clean, "no") {
				insecureCfg++
			}
		}
	}
	return weakPerm, insecureCfg
}

func (c *Collector) scanLargeFileGrowth() (count int, growthBytes int64) {
	roots := []string{"/var/log", "/var/lib", "/tmp", "/var/tmp"}
	seen := 0
	nowSizes := make(map[string]int64, 128)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if depthFromRoot(root, path) > 4 {
					return filepath.SkipDir
				}
				return nil
			}
			if seen >= securityMaxWalkEntries {
				return filepath.SkipDir
			}
			seen++
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			size := info.Size()
			if size < securityFileSizeLimitB {
				return nil
			}
			count++
			nowSizes[path] = size
			if prev, ok := c.securityFileSizes[path]; ok && size > prev {
				growthBytes += size - prev
			}
			return nil
		})
	}
	c.securityFileSizes = nowSizes
	return count, growthBytes
}

func (c *Collector) scanNetworkExposure(now time.Time) (listeningPorts, unexpectedPorts, stalePorts int, suspiciousOutbound int, synBacklogPressure float64) {
	listening := collectListeningPorts()
	for _, port := range listening {
		listeningPorts++
		if !knownServicePort(port) {
			unexpectedPorts++
		}
		firstSeen, ok := c.securityPortFirstSeen[port]
		if !ok {
			c.securityPortFirstSeen[port] = now
			continue
		}
		if now.Sub(firstSeen) >= securityStalePortAge {
			stalePorts++
		}
	}

	activePorts := make(map[int]struct{}, len(listening))
	for _, port := range listening {
		activePorts[port] = struct{}{}
	}
	for port := range c.securityPortFirstSeen {
		if _, ok := activePorts[port]; !ok {
			delete(c.securityPortFirstSeen, port)
		}
	}

	suspiciousOutbound = collectSuspiciousOutboundDestinations()
	synBacklogPressure = estimateSynBacklogPressure()
	return listeningPorts, unexpectedPorts, stalePorts, suspiciousOutbound, synBacklogPressure
}

func collectListeningPorts() []int {
	ports := map[int]struct{}{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum == 1 {
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 {
				continue
			}
			if strings.ToUpper(fields[3]) != "0A" { // LISTEN
				continue
			}
			local := strings.Split(fields[1], ":")
			if len(local) != 2 {
				continue
			}
			port, parseErr := strconv.ParseInt(local[1], 16, 32)
			if parseErr != nil {
				continue
			}
			ports[int(port)] = struct{}{}
		}
		_ = f.Close()
	}
	out := make([]int, 0, len(ports))
	for port := range ports {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func collectSuspiciousOutboundDestinations() int {
	ips := map[string]struct{}{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum == 1 {
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 {
				continue
			}
			if strings.ToUpper(fields[3]) != "01" { // ESTABLISHED
				continue
			}
			remote := strings.Split(fields[2], ":")
			if len(remote) != 2 {
				continue
			}
			ip := decodeProcIP(remote[0], strings.HasSuffix(path, "tcp6"))
			if ip == nil {
				continue
			}
			if isSuspiciousRemoteIP(ip) {
				ips[ip.String()] = struct{}{}
			}
		}
		_ = f.Close()
	}
	return len(ips)
}

func decodeProcIP(raw string, v6 bool) net.IP {
	if !v6 {
		if len(raw) != 8 {
			return nil
		}
		b1, err1 := strconv.ParseUint(raw[6:8], 16, 8)
		b2, err2 := strconv.ParseUint(raw[4:6], 16, 8)
		b3, err3 := strconv.ParseUint(raw[2:4], 16, 8)
		b4, err4 := strconv.ParseUint(raw[0:2], 16, 8)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			return nil
		}
		return net.IPv4(byte(b1), byte(b2), byte(b3), byte(b4))
	}
	if len(raw) != 32 {
		return nil
	}
	ip := make(net.IP, net.IPv6len)
	for i := 0; i < 16; i++ {
		v, err := strconv.ParseUint(raw[(15-i)*2:(15-i)*2+2], 16, 8)
		if err != nil {
			return nil
		}
		ip[i] = byte(v)
	}
	return ip
}

func isSuspiciousRemoteIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		privateCIDRs := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16"}
		for _, cidr := range privateCIDRs {
			_, block, _ := net.ParseCIDR(cidr)
			if block != nil && block.Contains(ip4) {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(strings.ToLower(ip.String()), "fe80") {
		return false
	}
	return true
}

func estimateSynBacklogPressure() float64 {
	stateCounts := map[string]int{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum == 1 {
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 {
				continue
			}
			stateCounts[strings.ToUpper(fields[3])]++
		}
		_ = f.Close()
	}
	synRecv := stateCounts["03"]
	listen := stateCounts["0A"]
	if synRecv == 0 {
		return 0
	}
	denom := float64(listen + 1)
	return float64(synRecv) / denom
}

func scanSysctlRisky() int {
	targets := map[string]func(int64) bool{
		"/proc/sys/net/ipv4/conf/all/accept_redirects": func(v int64) bool { return v == 1 },
		"/proc/sys/net/ipv4/conf/all/send_redirects":   func(v int64) bool { return v == 1 },
		"/proc/sys/net/ipv4/ip_forward":                func(v int64) bool { return v == 1 },
		"/proc/sys/kernel/kptr_restrict":               func(v int64) bool { return v < 2 },
		"/proc/sys/kernel/randomize_va_space":          func(v int64) bool { return v < 2 },
		"/proc/sys/kernel/yama/ptrace_scope":           func(v int64) bool { return v < 1 },
	}
	count := 0
	for path, predicate := range targets {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value, parseErr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if parseErr != nil {
			continue
		}
		if predicate(value) {
			count++
		}
	}
	return count
}

func scanFirewallDisabled() int {
	hasIPTables := false
	hasNFT := false
	if data, err := os.ReadFile("/proc/net/ip_tables_names"); err == nil {
		hasIPTables = strings.TrimSpace(string(data)) != ""
	}
	if data, err := os.ReadFile("/proc/net/nf_tables"); err == nil {
		hasNFT = strings.TrimSpace(string(data)) != ""
	}
	if hasIPTables || hasNFT {
		return 0
	}
	return 1
}

func scanMACStatus() (selinuxDisabled int, apparmorDisabled int) {
	if data, err := os.ReadFile("/sys/fs/selinux/enforce"); err == nil {
		if strings.TrimSpace(string(data)) != "1" {
			selinuxDisabled = 1
		}
	} else {
		selinuxDisabled = 1
	}

	if data, err := os.ReadFile("/sys/module/apparmor/parameters/enabled"); err == nil {
		trimmed := strings.TrimSpace(strings.ToLower(string(data)))
		if trimmed != "y" && trimmed != "yes" && trimmed != "1" {
			apparmorDisabled = 1
		}
	} else {
		apparmorDisabled = 1
	}
	return selinuxDisabled, apparmorDisabled
}

func scanPrivilegedProcessPaths() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	count := 0
	processed := 0
	for _, entry := range entries {
		if processed >= 4096 {
			break
		}
		if !entry.IsDir() {
			continue
		}
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 0 {
			continue
		}
		processed++
		statusPath := filepath.Join("/proc", entry.Name(), "status")
		data, readErr := os.ReadFile(statusPath)
		if readErr != nil {
			continue
		}
		if !hasRootUID(data) {
			continue
		}
		exePath, exeErr := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if exeErr != nil {
			continue
		}
		if isExpectedPrivilegedPath(exePath) {
			continue
		}
		count++
	}
	return count
}

func hasRootUID(status []byte) bool {
	scanner := bufio.NewScanner(strings.NewReader(string(status)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return false
		}
		return fields[1] == "0"
	}
	return false
}

func isExpectedPrivilegedPath(path string) bool {
	allowedPrefixes := []string{"/usr/bin/", "/usr/sbin/", "/bin/", "/sbin/", "/lib/systemd/", "/snap/"}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func scanSchedulerUnits() (cronAnomalies int, systemdUnknown int) {
	if entries, err := os.ReadDir("/etc/cron.d"); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			if strings.Contains(name, "tmp") || strings.Contains(name, "backup") || strings.Contains(name, "unknown") {
				cronAnomalies++
			}
			if info, infoErr := entry.Info(); infoErr == nil {
				if info.Mode().Perm()&0o002 != 0 {
					cronAnomalies++
				}
			}
		}
	}

	if entries, err := os.ReadDir("/etc/systemd/system"); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			if !strings.HasSuffix(name, ".service") && !strings.HasSuffix(name, ".timer") {
				continue
			}
			if strings.Contains(name, "tmp") || strings.Contains(name, "debug") || strings.Contains(name, "unknown") {
				systemdUnknown++
			}
		}
	}
	return cronAnomalies, systemdUnknown
}

func scanContainerEscapeRisk() (containerPrivileged int, containerCapabilityRisk int) {
	status, err := os.ReadFile("/proc/1/status")
	if err != nil {
		return 0, 0
	}
	scanner := bufio.NewScanner(strings.NewReader(string(status)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CapEff:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if fields[1] != "0000000000000000" {
				containerCapabilityRisk = 1
			}
		}
	}
	if data, cErr := os.ReadFile("/proc/1/cgroup"); cErr == nil {
		body := strings.ToLower(string(data))
		if strings.Contains(body, "docker") || strings.Contains(body, "kubepods") || strings.Contains(body, "containerd") {
			if containerCapabilityRisk > 0 {
				containerPrivileged = 1
			}
		}
	}
	return containerPrivileged, containerCapabilityRisk
}

func packageVulnerabilityHint() float64 {
	raw := strings.TrimSpace(os.Getenv("SRE_COLLECTOR_SECURITY_PACKAGE_VULN_COUNT"))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func knownServicePort(port int) bool {
	known := map[int]struct{}{
		22: {}, 53: {}, 80: {}, 123: {}, 443: {}, 2379: {}, 2380: {}, 3000: {}, 3306: {}, 5432: {},
		6379: {}, 6443: {}, 8080: {}, 8443: {}, 9090: {}, 9100: {}, 10250: {}, 10257: {}, 10259: {},
	}
	_, ok := known[port]
	return ok
}

func depthFromRoot(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 0
	}
	if rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func (c *Collector) SecurityAuditInfo() string {
	return fmt.Sprintf("security_audit_interval=%s last_security_collect=%s cached_metrics=%d", c.securityAuditInterval, c.lastSecurityCollect.Format(time.RFC3339), len(c.securityMetricsCache))
}
