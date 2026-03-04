package utils

import (
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalLogger *zap.Logger
	globalSugar  *zap.SugaredLogger
	once         sync.Once
)

// Config defines the logging configuration
type Config struct {
	Level      string
	Format     string // json or text
	Output     string // stdout or file path
	WithCaller bool
}

// InitLogger initializes the global logger
func InitLogger(cfg Config) error {
	var err error
	once.Do(func() {
		err = initLoggerInternal(cfg)
	})
	return err
}

func initLoggerInternal(cfg Config) error {
	// Parse log level
	level := zapcore.InfoLevel
	if cfg.Level != "" {
		if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
			return err
		}
	}

	// Configure encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	var encoder zapcore.Encoder
	if cfg.Format == "text" {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	// Configure output
	var writer zapcore.WriteSyncer
	if cfg.Output == "stdout" || cfg.Output == "" {
		writer = zapcore.AddSync(os.Stdout)
	} else {
		file, err := os.OpenFile(cfg.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		writer = zapcore.AddSync(file)
	}

	// Build core
	core := zapcore.NewCore(
		encoder,
		writer,
		level,
	)

	// Create logger with optional caller
	opts := []zap.Option{zap.AddStacktrace(zapcore.ErrorLevel)}
	if cfg.WithCaller {
		opts = append(opts, zap.AddCaller())
	}
	globalLogger = zap.New(core, opts...)
	globalSugar = globalLogger.Sugar()

	return nil
}

// GetLogger returns the global logger
func GetLogger() *zap.Logger {
	if globalLogger == nil {
		InitLogger(Config{Level: "info", Format: "json"})
	}
	return globalLogger
}

// GetSugaredLogger returns the global sugared logger
func GetSugaredLogger() *zap.SugaredLogger {
	if globalSugar == nil {
		InitLogger(Config{Level: "info", Format: "json"})
	}
	return globalSugar
}

// With creates a child logger with additional fields
func With(fields ...zap.Field) *zap.Logger {
	return GetLogger().With(fields...)
}

// Debug logs a debug message
func Debug(msg string, fields ...zap.Field) {
	GetLogger().Debug(msg, fields...)
}

// Info logs an info message
func Info(msg string, fields ...zap.Field) {
	GetLogger().Info(msg, fields...)
}

// Warn logs a warning message
func Warn(msg string, fields ...zap.Field) {
	GetLogger().Warn(msg, fields...)
}

// Error logs an error message
func Error(msg string, fields ...zap.Field) {
	GetLogger().Error(msg, fields...)
}

// Fatal logs a fatal message and exits
func Fatal(msg string, fields ...zap.Field) {
	GetLogger().Fatal(msg, fields...)
}

// Sync flushes any buffered log entries
func Sync() error {
	if globalLogger != nil {
		return globalLogger.Sync()
	}
	return nil
}
