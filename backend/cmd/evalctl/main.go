package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/evaluation"
)

func main() {
	var (
		scope                     = flag.String("scope", string(eval.ScopeFast), "evaluation scope: fast, regression, benchmark")
		format                    = flag.String("format", "text", "output format: text, table, or json")
		repoRoot                  = flag.String("repo-root", "", "override repository root used to load eval data")
		judgeLLM                  = flag.Bool("judge-llm", false, "grade anomaly explanations with the configured LLM provider")
		judgeLimit                = flag.Int("judge-limit", 0, "limit the number of anomaly cases sent to the LLM judge; 0 means all")
		judgeBatch                = flag.Int("judge-batch-size", 5, "number of anomaly cases to grade per LLM judge call")
		systemPerf                = flag.Bool("system-perf", false, "run the end-to-end multi-agent system performance evaluator")
		comparePath               = flag.String("compare", "", "compare the current system performance report against a saved baseline JSON report")
		systemPerfVariant         = flag.String("variant", "", "optional label for the current system-performance configuration")
		systemPerfCases           = flag.String("system-perf-cases", "", "optional comma-separated list of system-performance case ids to run")
		systemPerfReplayRuns      = flag.Int("system-perf-replay-runs", 2, "number of repeated runs per system-performance case")
		systemPerfMessageProtocol = flag.String("system-perf-message-protocol", "default", "message protocol mode for system-perf: default, on, off")
		systemPerfValidationAgent = flag.String("system-perf-validation-agent", "default", "validation agent mode for system-perf: default, on, off")
	)
	flag.Parse()

	if *systemPerf {
		report, err := evaluation.RunSystemPerformance(context.Background(), evaluation.SystemPerformanceOptions{
			Scope:                       eval.Scope(*scope),
			RepoRoot:                    *repoRoot,
			Variant:                     *systemPerfVariant,
			ComparePath:                 *comparePath,
			ReplayRuns:                  *systemPerfReplayRuns,
			CaseIDs:                     splitCSV(*systemPerfCases),
			AgentMessageProtocolEnabled: parseBoolOverride(*systemPerfMessageProtocol),
			ValidationAgentEnabled:      parseBoolOverride(*systemPerfValidationAgent),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "system performance eval failed: %v\n", err)
			os.Exit(1)
		}
		switch *format {
		case "json":
			raw, err := report.JSON()
			if err != nil {
				fmt.Fprintf(os.Stderr, "encode system performance report: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(string(raw))
		default:
			fmt.Println(report.Text())
		}
		if !report.Passed {
			os.Exit(2)
		}
		return
	}

	report, err := eval.Run(context.Background(), eval.RunOptions{
		Scope:             eval.Scope(*scope),
		RepoRoot:          *repoRoot,
		JudgeExplanations: *judgeLLM,
		JudgeCaseLimit:    *judgeLimit,
		JudgeBatchSize:    *judgeBatch,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval failed: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "json":
		raw, err := report.JSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(raw))
	default:
		fmt.Println(report.Text())
	}

	if !report.Passed {
		os.Exit(2)
	}
}

func parseBoolOverride(raw string) *bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "enabled":
		value := true
		return &value
	case "off", "false", "disabled":
		value := false
		return &value
	default:
		return nil
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, item := range parts {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
