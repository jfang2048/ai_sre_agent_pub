package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	"go.uber.org/zap"
)

func main() {
	logger := zap.NewNop()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cfg := rag.ConfigFromEnv(rag.DefaultConfig())
	cfg.Enabled = true

	service, err := rag.NewService(cfg, logger)
	if err != nil {
		fail(err)
	}

	switch os.Args[1] {
	case "status":
		printJSON(service.Stats())
	case "rebuild":
		stats, err := service.Rebuild(context.Background())
		if err != nil {
			fail(err)
		}
		printJSON(stats)
	case "update":
		stats, err := service.Update(context.Background())
		if err != nil {
			fail(err)
		}
		printJSON(stats)
	case "query":
		queryFlags := flag.NewFlagSet("query", flag.ExitOnError)
		queryText := queryFlags.String("q", "", "query text")
		topK := queryFlags.Int("k", cfg.TopK, "top-k hits")
		_ = queryFlags.Parse(os.Args[2:])
		if *queryText == "" {
			fail(fmt.Errorf("query text is required"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := service.Query(ctx, rag.QueryRequest{Query: *queryText, TopK: *topK})
		if err != nil {
			fail(err)
		}
		printJSON(result)
	case "doc":
		docFlags := flag.NewFlagSet("doc", flag.ExitOnError)
		id := docFlags.String("id", "", "document or chunk id")
		_ = docFlags.Parse(os.Args[2:])
		if *id == "" {
			fail(fmt.Errorf("document id is required"))
		}
		record, ok := service.Document(*id)
		if !ok {
			fail(fmt.Errorf("document not found"))
		}
		printJSON(record)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ragctl <status|rebuild|update|query|doc> [flags]")
	fmt.Fprintln(os.Stderr, "env: SRE_AGENT_RAG_* controls dataset/index/embedding settings")
}

func printJSON(payload any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}
