package helpers

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"

	"github.com/rh-ecosystem-edge/eco-gotests/tests/cnf/ran/ibipreinstall/internal/tsparams"
)

const (
	scpSubprocessTimeout = 30 * time.Minute
	sshSubprocessTimeout = 3 * time.Minute
)

// SCPToHTTPServer copies a local file to the HTTP server using scp.
func SCPToHTTPServer(parentCtx context.Context, srcPath, user, host, dir, sshKeyPath string) error {
	destFile := filepath.Base(srcPath)
	destPath := filepath.Join(dir, destFile)

	klog.V(tsparams.LogLevel).Infof("Copying %s to %s@%s:%s", srcPath, user, host, destPath)

	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", sshKeyPath,
		srcPath,
		fmt.Sprintf("%s@%s:%s", user, host, destPath),
	}

	ctx, cancel := context.WithTimeout(parentCtx, scpSubprocessTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "scp", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("scp timed out after %v: %w, output: %s", scpSubprocessTimeout, err, string(output))
		}

		return fmt.Errorf("scp failed: %w, output: %s", err, string(output))
	}

	klog.V(tsparams.LogLevel).Infof("Successfully copied file to HTTP server")

	return nil
}

// SSHExec executes a command on a remote host via SSH.
func SSHExec(parentCtx context.Context, host, user, sshKeyPath, command string) (string, error) {
	klog.V(tsparams.LogLevel).Infof("Executing on %s@%s: %s", user, host, command)

	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=10",
		"-i", sshKeyPath,
		fmt.Sprintf("%s@%s", user, host),
		command,
	}

	ctx, cancel := context.WithTimeout(parentCtx, sshSubprocessTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return string(output), fmt.Errorf(
				"ssh timed out after %v: %w, output: %s", sshSubprocessTimeout, err, string(output))
		}

		return string(output), fmt.Errorf("ssh failed: %w, output: %s", err, string(output))
	}

	return string(output), nil
}
