package controller

import (
	"fmt"
	"net"
	"strings"

	"go.uber.org/zap"
)

// listenWithFallback tries to listen on the requested address; on common bind errors
// (permission, address in use) it falls back to an ephemeral port on the same host.
func listenWithFallback(addr string, logger *zap.Logger, name string) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, ln.Addr().String(), nil
	}

	if !isRetryableListenErr(err) {
		return nil, "", err
	}

	host, _, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		host = ""
	}
	fallback := net.JoinHostPort(host, "0")

	ln, errFallback := net.Listen("tcp", fallback)
	if errFallback != nil {
		// Return the original error to keep context.
		return nil, "", err
	}

	resolved := ln.Addr().String()
	logger.Warn(fmt.Sprintf("%s listen failed; using random port", name),
		zap.String("requested", addr),
		zap.String("resolved", resolved),
		zap.Error(err))
	return ln, resolved, nil
}

func isRetryableListenErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "permission") ||
		strings.Contains(msg, "not permitted")
}

// dialAddr turns a possibly-unspecified listen address into a dialable one.
// E.g. ":8080" or "0.0.0.0:8080" -> "127.0.0.1:8080".
func DialAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
