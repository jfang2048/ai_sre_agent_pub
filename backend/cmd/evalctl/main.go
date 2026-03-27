package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

func main() {
	var (
		scope      = flag.String("scope", string(eval.ScopeFast), "evaluation scope: fast, regression, benchmark")
		format     = flag.String("format", "text", "output format: text or json")
		repoRoot   = flag.String("repo-root", "", "override repository root used to load eval data")
		judgeLLM   = flag.Bool("judge-llm", false, "grade anomaly explanations with the configured LLM provider")
		judgeLimit = flag.Int("judge-limit", 0, "limit the number of anomaly cases sent to the LLM judge; 0 means all")
		judgeBatch = flag.Int("judge-batch-size", 5, "number of anomaly cases to grade per LLM judge call")
	)
	flag.Parse()

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
