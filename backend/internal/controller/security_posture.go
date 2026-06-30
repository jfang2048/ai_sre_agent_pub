package controller

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
)

func enforceControllerSecurityPosture(cfg Config, resolved ResolvedAuthConfig, logger *zap.Logger) (ResolvedAuthConfig, error) {
	mode := normalizeControllerDeploymentMode(cfg.Deployment.Mode)
	if mode == "" {
		mode = defaultDeploymentMode
	}
	resolved.DeploymentMode = mode
	if resolved.Enabled {
		return resolved, nil
	}
	if mode == defaultDeploymentMode {
		resolved.LocalDevBypass = true
		if logger != nil {
			logger.Info("controller api authentication disabled for local development",
				zap.String("deployment_mode", mode))
		}
		return resolved, nil
	}
	if controllerInsecureOverrideEnabled(cfg, resolved) {
		resolved.InsecureOverride = true
		if logger != nil {
			logger.Warn("controller api authentication disabled outside local-dev via explicit insecure override",
				zap.String("deployment_mode", mode))
		}
		return resolved, nil
	}
	return ResolvedAuthConfig{}, fmt.Errorf(
		"controller auth is disabled for deployment mode %q; enable auth or set deployment.insecure_override=true (or auth.allow_insecure_disable=true) to acknowledge the insecure deployment",
		strings.TrimSpace(mode),
	)
}
