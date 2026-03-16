package utils

import (
	"fmt"
	"runtime"
)

// ErrorCode represents a category of error.
type ErrorCode string

const (
	ErrCodeUnknown          ErrorCode = "unknown"
	ErrCodeInvalidArg       ErrorCode = "invalid_argument"
	ErrCodeNotFound         ErrorCode = "not_found"
	ErrCodeInitFailed       ErrorCode = "init_failed"
	ErrCodeStartFailed      ErrorCode = "start_failed"
	ErrCodeInternal         ErrorCode = "internal"
	ErrCodeValidationFailed ErrorCode = "validation_failed"
	ErrCodeAlreadyExists    ErrorCode = "already_exists"
	ErrCodeUnauthorized     ErrorCode = "unauthorized"
	ErrCodeForbidden        ErrorCode = "forbidden"
	ErrCodeUnavailable      ErrorCode = "unavailable"
	ErrCodeTimeout          ErrorCode = "timeout"
	ErrCodeNotImplemented   ErrorCode = "not_implemented"
	ErrCodeK8sClientFailed  ErrorCode = "k8s_client_failed"
	ErrCodeStorageOpFailed  ErrorCode = "storage_op_failed"
	ErrCodeParseFailed      ErrorCode = "parse_failed"
	ErrCodeKVMClientFailed  ErrorCode = "kvm_client_failed"
	ErrCodeKVMNullDomain    ErrorCode = "kvm_null_domain"
	ErrCodeKVMOpFailed      ErrorCode = "kvm_op_failed"
)

// TraceableError wraps a standard error with stack trace and context.
type TraceableError struct {
	Err   error
	Code  ErrorCode
	Msg   string
	Op    string
	Stack []uintptr
}

func (e *TraceableError) Error() string {
	msg := e.Msg
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Op != "" {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Op, msg)
	}
	return fmt.Sprintf("[%s] %v", e.Code, msg)
}

func (e *TraceableError) Unwrap() error {
	return e.Err
}

// Wrap creates a new TraceableError.
func Wrap(err error, code ErrorCode, msg string) error {
	if err == nil {
		return nil
	}

	stack := make([]uintptr, 32)
	n := runtime.Callers(2, stack)

	return &TraceableError{
		Err:   err,
		Code:  code,
		Msg:   msg,
		Stack: stack[:n],
	}
}

// Errorf creates a new TraceableError from a format string.
func Errorf(code ErrorCode, format string, args ...interface{}) error {
	stack := make([]uintptr, 32)
	n := runtime.Callers(2, stack)

	return &TraceableError{
		Err:   fmt.Errorf(format, args...),
		Code:  code,
		Stack: stack[:n],
	}
}
