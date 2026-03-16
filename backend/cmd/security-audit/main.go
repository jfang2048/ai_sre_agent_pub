package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/security"
)

func main() {
	var (
		root   = flag.String("root", ".", "Repository root to audit")
		format = flag.String("format", "markdown", "Output format: markdown|json")
		output = flag.String("output", "", "Output file path (default: stdout)")
		failOn = flag.String("fail-on", "fail", "Fail threshold: none|warn|fail")
	)
	flag.Parse()

	report, err := security.RunRuntimeSecurityAudit(security.RuntimeAuditOptions{RepoRoot: *root})
	if err != nil {
		fmt.Fprintf(os.Stderr, "security-audit error: %v\n", err)
		os.Exit(1)
	}

	body, err := security.FormatRuntimeSecurityReport(report, *format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "security-audit format error: %v\n", err)
		os.Exit(1)
	}

	if strings.TrimSpace(*output) == "" {
		_, _ = os.Stdout.Write(body)
	} else {
		if err := os.WriteFile(*output, body, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "security-audit write error: %v\n", err)
			os.Exit(1)
		}
	}

	switch strings.ToLower(strings.TrimSpace(*failOn)) {
	case "none":
		return
	case "warn":
		if report.HasStatus(security.RuntimeStatusFail) || report.HasStatus(security.RuntimeStatusWarn) {
			os.Exit(2)
		}
	case "fail", "":
		if report.HasStatus(security.RuntimeStatusFail) {
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "invalid --fail-on value %q (expected none|warn|fail)\n", *failOn)
		os.Exit(1)
	}
}
