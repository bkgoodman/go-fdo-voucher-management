// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ExternalCommandExecutor handles execution of external commands with variable substitution
type ExternalCommandExecutor struct {
	commandTemplate string
	timeout         time.Duration
}

// NewExternalCommandExecutor creates a new external command executor
func NewExternalCommandExecutor(commandTemplate string, timeout time.Duration) *ExternalCommandExecutor {
	return &ExternalCommandExecutor{
		commandTemplate: commandTemplate,
		timeout:         timeout,
	}
}

// Execute runs the external command with variable substitution
func (e *ExternalCommandExecutor) Execute(ctx context.Context, variables map[string]string) (string, error) {
	command := e.commandTemplate
	for key, value := range variables {
		command = strings.ReplaceAll(command, "{"+key+"}", value)
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("external command failed: %w, output: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}
