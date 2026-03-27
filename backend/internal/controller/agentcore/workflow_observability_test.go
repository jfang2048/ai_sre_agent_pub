package agent

import (
	"bytes"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestWorkflowMetaLoggerIncludesTraceID(t *testing.T) {
	var buf bytes.Buffer
	engine := &WorkflowEngine{
		metaLogger: newWorkflowMetaLoggerWithWriter(&buf),
	}

	engine.logWorkflowEvent(zap.InfoLevel, "workflow.audit", map[string]any{
		"trace_id":      "trace-123",
		"workflow_type": "rca",
		"status":        "success",
	})

	out := buf.String()
	if !strings.Contains(out, `"trace_id":"trace-123"`) {
		t.Fatalf("expected trace_id in structured log output, got %s", out)
	}
	if !strings.Contains(out, `"event":"workflow.audit"`) {
		t.Fatalf("expected event in structured log output, got %s", out)
	}
}
