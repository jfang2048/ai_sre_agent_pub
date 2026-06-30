#!/usr/bin/env bash

repo_version() {
  local root="${1:?repository root required}"
  tr -d '[:space:]' < "${root}/VERSION" 2>/dev/null || echo "v0.9"
}
