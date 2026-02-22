package observability

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LoggerKeys are constants for structured logging keys
type LoggerKeys string

const (
	KeyComponent    LoggerKeys = "component"
	KeyAgentName    LoggerKeys = "agent_name"
	KeySource       LoggerKeys = "source"
	KeyMetric       LoggerKeys = "metric"
	KeyAction       LoggerKeys = "action"
	KeyTarget       LoggerKeys = "target"
	KeyDuration     LoggerKeys = "duration"
	KeyError        LoggerKeys = "error"
	KeyPredictionID LoggerKeys = "prediction_id"
	KeyAlertID      LoggerKeys = "alert_id"
	KeySLOName      LoggerKeys = "slo_name"
)

// ContextLogger adds context-aware logging
type ContextLogger struct {
	logger *zap.Logger
}

// NewContextLogger creates a new context logger
func NewContextLogger(logger *zap.Logger) *ContextLogger {
	return &ContextLogger{logger: logger}
}

// With creates a new logger with additional fields
func (l *ContextLogger) With(fields ...zap.Field) *ContextLogger {
	return &ContextLogger{logger: l.logger.With(fields...)}
}

// WithComponent adds a component field
func (l *ContextLogger) WithComponent(name string) *ContextLogger {
	return l.With(zap.String(string(KeyComponent), name))
}

// WithSource adds a source field
func (l *ContextLogger) WithSource(source string) *ContextLogger {
	return l.With(zap.String(string(KeySource), source))
}

// WithMetric adds a metric field
func (l *ContextLogger) WithMetric(name string) *ContextLogger {
	return l.With(zap.String(string(KeyMetric), name))
}

// WithAction adds an action field
func (l *ContextLogger) WithAction(action string) *ContextLogger {
	return l.With(zap.String(string(KeyAction), action))
}

// WithTarget adds a target field
func (l *ContextLogger) WithTarget(target string) *ContextLogger {
	return l.With(zap.String(string(KeyTarget), target))
}

// WithError adds an error field
func (l *ContextLogger) WithError(err error) *ContextLogger {
	return l.With(zap.Error(err))
}

// Debug logs a debug message
func (l *ContextLogger) Debug(msg string, fields ...zap.Field) {
	l.logger.Debug(msg, fields...)
}

// Info logs an info message
func (l *ContextLogger) Info(msg string, fields ...zap.Field) {
	l.logger.Info(msg, fields...)
}

// Warn logs a warning message
func (l *ContextLogger) Warn(msg string, fields ...zap.Field) {
	l.logger.Warn(msg, fields...)
}

// Error logs an error message
func (l *ContextLogger) Error(msg string, fields ...zap.Field) {
	l.logger.Error(msg, fields...)
}

// Fatal logs a fatal message and exits
func (l *ContextLogger) Fatal(msg string, fields ...zap.Field) {
	l.logger.Fatal(msg, fields...)
}

// Logger returns the underlying zap logger
func (l *ContextLogger) Logger() *zap.Logger {
	return l.logger
}

// LogActionStart logs the start of an action
func (l *ContextLogger) LogActionStart(action, target string) zap.Field {
	l.Info("action started",
		zap.String(string(KeyAction), action),
		zap.String(string(KeyTarget), target),
	)
	return zap.String(string(KeyAction), action)
}

// LogActionComplete logs the completion of an action
func (l *ContextLogger) LogActionComplete(action, target string, duration string, err error) {
	fields := []zap.Field{
		zap.String(string(KeyAction), action),
		zap.String(string(KeyTarget), target),
		zap.String(string(KeyDuration), duration),
	}
	if err != nil {
		l.Error("action failed", fields...)
	} else {
		l.Info("action completed", fields...)
	}
}

// LogPrediction logs a prediction
func (l *ContextLogger) LogPrediction(predType string, confidence float64, willViolate bool) {
	l.Info("prediction made",
		zap.String("type", predType),
		zap.Float64("confidence", confidence),
		zap.Bool("will_violate", willViolate),
	)
}

// LogSLOViolation logs an SLO violation
func (l *ContextLogger) LogSLOViolation(sloName string, currentValue, target float64) {
	l.Error("SLO violation detected",
		zap.String(string(KeySLOName), sloName),
		zap.Float64("current_value", currentValue),
		zap.Float64("target", target),
	)
}

// LogAlertFired logs an alert firing
func (l *ContextLogger) LogAlertFired(alertName, severity string) {
	l.Warn("alert fired",
		zap.String("alert", alertName),
		zap.String("severity", severity),
	)
}

// NewProductionLogger creates a production logger
func NewProductionLogger(level zapcore.Level) (*zap.Logger, error) {
	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
	return config.Build()
}

// NewDevelopmentLogger creates a development logger
func NewDevelopmentLogger() (*zap.Logger, error) {
	return zap.NewDevelopment()
}

// NewNopLogger creates a no-op logger
func NewNopLogger() *zap.Logger {
	return zap.NewNop()
}

// ParseLogLevel parses a log level string
func ParseLogLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zap.DebugLevel
	case "info":
		return zap.InfoLevel
	case "warn":
		return zap.WarnLevel
	case "error":
		return zap.ErrorLevel
	case "fatal":
		return zap.FatalLevel
	default:
		return zap.InfoLevel
	}
}

// ConfigureLogger configures a logger from environment variables
func ConfigureLogger() (*zap.Logger, error) {
	logLevel := os.Getenv("LOG_LEVEL")
	logFormat := os.Getenv("LOG_FORMAT")

	level := ParseLogLevel(logLevel)

	if logFormat == "text" || logFormat == "console" {
		return NewDevelopmentLogger()
	}
	return NewProductionLogger(level)
}

// MustCreateLogger creates a logger or panics
func MustCreateLogger(level string) *zap.Logger {
	logger, err := NewProductionLogger(ParseLogLevel(level))
	if err != nil {
		panic(fmt.Sprintf("failed to create logger: %v", err))
	}
	return logger
}

// NewLogger creates a logger with specified level and format.
// level: debug, info, warn, error
// format: json (production) or text/console (development)
func NewLogger(level, format string) (*zap.Logger, error) {
	zapLevel := ParseLogLevel(level)

	if format == "text" || format == "console" {
		return NewDevelopmentLogger()
	}
	return NewProductionLogger(zapLevel)
}

// WithContext adds context to the logger
func WithContext(ctx context.Context, logger *zap.Logger) *zap.Logger {
	// Add trace/span IDs if available
	if traceID := TraceID(ctx); traceID != "" {
		logger = logger.With(zap.String("trace_id", traceID))
	}
	if spanID := SpanID(ctx); spanID != "" {
		logger = logger.With(zap.String("span_id", spanID))
	}
	return logger
}
