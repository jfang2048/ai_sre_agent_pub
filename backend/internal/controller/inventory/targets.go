package inventory

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// TargetsFile is a hand-editable controller-side inventory of known collectors.
type TargetsFile struct {
	Collectors []rawStaticProbe `yaml:"collectors" json:"collectors"`
}

type rawStaticProbe struct {
	ID       string            `yaml:"id"`
	Name     string            `yaml:"name"`
	Hostname string            `yaml:"hostname"`
	Address  string            `yaml:"address"`
	Port     int               `yaml:"port"`
	Enabled  *bool             `yaml:"enabled"`
	Labels   map[string]string `yaml:"labels"`
	Tags     []string          `yaml:"tags"`
	Auth     TargetAuth        `yaml:"auth"`
	Metadata map[string]string `yaml:"metadata"`
}

// LoadTargetsFile parses a dedicated collector inventory file.
func LoadTargetsFile(path string) ([]StaticProbe, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read controller targets file %q: %w", path, err)
	}

	var doc TargetsFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse controller targets file %q: %w", path, err)
	}

	out := make([]StaticProbe, 0, len(doc.Collectors))
	for i, raw := range doc.Collectors {
		normalized, err := normalizeStaticProbe(StaticProbe{
			ID:       raw.ID,
			Name:     raw.Name,
			Hostname: raw.Hostname,
			Address:  raw.Address,
			Port:     raw.Port,
			Enabled:  raw.Enabled == nil || *raw.Enabled,
			Labels:   raw.Labels,
			Tags:     raw.Tags,
			Auth:     raw.Auth,
			Metadata: raw.Metadata,
		})
		if err != nil {
			return nil, fmt.Errorf("controller target %d: %w", i, err)
		}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeStaticProbe(in StaticProbe) (StaticProbe, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.Name = strings.TrimSpace(in.Name)
	in.Hostname = strings.TrimSpace(in.Hostname)
	in.Address = strings.TrimSpace(in.Address)
	in.Auth.Mode = strings.TrimSpace(in.Auth.Mode)
	in.Auth.ServerName = strings.TrimSpace(in.Auth.ServerName)
	in.Auth.TokenEnv = strings.TrimSpace(in.Auth.TokenEnv)
	in.Auth.Description = strings.TrimSpace(in.Auth.Description)

	hostPart := ""
	portPart := ""
	if in.Address != "" {
		if host, port, err := net.SplitHostPort(in.Address); err == nil {
			hostPart = strings.TrimSpace(host)
			portPart = strings.TrimSpace(port)
		} else if strings.Count(in.Address, ":") > 0 {
			return StaticProbe{}, fmt.Errorf("address %q must be host or host:port", in.Address)
		} else {
			hostPart = in.Address
		}
	}

	if in.Hostname == "" {
		in.Hostname = hostPart
	}
	if in.Address == "" {
		in.Address = firstNonEmpty(in.Hostname, hostPart)
	} else if hostPart != "" {
		in.Address = hostPart
	}

	if in.Port == 0 && portPart != "" {
		parsed, err := strconv.Atoi(portPart)
		if err != nil {
			return StaticProbe{}, fmt.Errorf("invalid port %q", portPart)
		}
		in.Port = parsed
	}
	if in.Port < 0 || in.Port > 65535 {
		return StaticProbe{}, fmt.Errorf("port %d must be between 0 and 65535", in.Port)
	}

	if in.ID == "" {
		in.ID = firstNonEmpty(in.Name, in.Hostname)
	}
	if in.ID == "" {
		if in.Port > 0 {
			in.ID = net.JoinHostPort(in.Address, strconv.Itoa(in.Port))
		} else {
			in.ID = in.Address
		}
	}
	if in.ID == "" {
		return StaticProbe{}, fmt.Errorf("id, hostname, or address is required")
	}
	if in.Name == "" {
		in.Name = in.ID
	}
	if in.Address == "" {
		return StaticProbe{}, fmt.Errorf("address or hostname is required")
	}
	if len(in.Tags) > 0 {
		in.Tags = cloneStrings(in.Tags)
	}
	in.Labels = cloneLabels(in.Labels)
	in.Metadata = cloneLabels(in.Metadata)
	return in, nil
}

func StaticProbeEndpoint(probe StaticProbe) string {
	host := strings.TrimSpace(probe.Address)
	if host == "" {
		host = strings.TrimSpace(probe.Hostname)
	}
	if host == "" {
		return ""
	}
	if probe.Port <= 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(probe.Port))
}
