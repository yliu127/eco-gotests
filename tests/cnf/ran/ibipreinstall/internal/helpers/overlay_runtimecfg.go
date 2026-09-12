package helpers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"github.com/rh-ecosystem-edge/eco-gotests/tests/cnf/ran/ibipreinstall/internal/tsparams"
)

const (
	// truncatedOverlayFileSize is the exact size seen when container overlay
	// extraction stops at 16MiB (256 × 64KiB pages) instead of writing the
	// full payload binary.
	truncatedOverlayFileSize int64 = 16 * 1024 * 1024

	// overlayRuntimecfgCheckCmd prints PAGE_SIZE and every overlay
	// runtimecfg size. Extra roots cover the live ISO (target disk under
	// /mnt) as well as the installed node's /var/lib/containers.
	overlayRuntimecfgCheckCmd = `echo "PAGE_SIZE=$(getconf PAGE_SIZE)"
for d in \
  /var/lib/containers/storage/overlay \
  /mnt/var/lib/containers/storage/overlay \
  /mnt/sysroot/var/lib/containers/storage/overlay \
  /sysroot/var/lib/containers/storage/overlay; do
  if [ -d "$d" ]; then
    echo "searching $d"
    sudo find "$d" -name runtimecfg -printf '%s %p\n'
  fi
done`
)

// CheckOverlayRuntimecfg SSHes to the spoke and inspects PAGE_SIZE plus
// overlay runtimecfg file sizes after preinstall. The raw command output is
// always returned so callers can print it. An error is returned if SSH fails,
// no overlay runtimecfg is found, or any copy is truncated at 16MiB.
func CheckOverlayRuntimecfg(parentCtx context.Context, host, user, sshKeyPath string) (string, error) {
	klog.V(tsparams.LogLevel).Infof("Checking PAGE_SIZE and overlay runtimecfg sizes on %s", host)

	output, err := SSHExec(parentCtx, host, user, sshKeyPath, overlayRuntimecfgCheckCmd)
	if err != nil {
		return output, fmt.Errorf("failed to inspect overlay runtimecfg on %s: %w", host, err)
	}

	err = validateOverlayRuntimecfgOutput(output)
	if err != nil {
		return output, err
	}

	return output, nil
}

// validateOverlayRuntimecfgOutput requires at least one overlay runtimecfg
// and rejects copies whose size is exactly 16MiB.
func validateOverlayRuntimecfgOutput(output string) error {
	var (
		found     bool
		truncated []string
	)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "PAGE_SIZE=") || strings.HasPrefix(line, "searching ") {
			continue
		}

		sizeStr, path, ok := strings.Cut(line, " ")
		if !ok || !strings.Contains(path, "runtimecfg") {
			continue
		}

		size, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			continue
		}

		found = true

		if size == truncatedOverlayFileSize {
			truncated = append(truncated, line)
		}
	}

	if !found {
		return fmt.Errorf("no overlay runtimecfg found after preinstall")
	}

	if len(truncated) > 0 {
		return fmt.Errorf("overlay runtimecfg truncated at 16MiB: %s", strings.Join(truncated, "; "))
	}

	return nil
}
