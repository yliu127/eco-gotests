package helpers

import (
	"context"
	"fmt"
	"regexp"
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

	// overlayPostPreinstallCheckScript collects PAGE_SIZE, restore-seed state,
	// overlay runtimecfg sizes, and empty link/lower metadata (the helix95 XFS
	// bug signal). Matches ztp-site-configs helix95-bug-logs README capture.
	overlayPostPreinstallCheckScript = `set +e
echo "PAGE_SIZE=$(getconf PAGE_SIZE)"
systemctl is-active install-rhcos-and-restore-seed.service 2>/dev/null || true
for d in /var/lib/containers/storage/overlay /mnt/var/lib/containers/storage/overlay /mnt/sysroot/var/lib/containers/storage/overlay /sysroot/var/lib/containers/storage/overlay; do
  if [ -d "$d" ]; then
    echo "searching $d"
    sudo find "$d" -name runtimecfg -printf '%s %p\n' 2>/dev/null
  fi
done
sudo python3 -c "
import os
root='/var/lib/containers/storage/overlay'
if not os.path.isdir(root):
    print('layers=0 empty_link=0')
else:
    n=e=0
    for name in os.listdir(root):
        if len(name)!=64:
            continue
        n+=1
        p=os.path.join(root,name,'link')
        if os.path.isfile(p) and os.path.getsize(p)==0:
            e+=1
    print(f'layers={n} empty_link={e}')
"
empty=$(sudo find /var/lib/containers/storage/overlay -mindepth 2 -maxdepth 2 \( -name link -o -name lower \) -size 0 2>/dev/null | wc -l)
echo "zero_size_link_lower=${empty}"
sudo find /var/lib/containers/storage/overlay -mindepth 2 -maxdepth 2 \( -name link -o -name lower \) -size 0 -printf '%s %p\n' 2>/dev/null | head -5
`
)

var overlayLayerStatsRE = regexp.MustCompile(`^layers=(\d+) empty_link=(\d+)$`)

// CheckOverlayRuntimecfg SSHes to the spoke after restore-seed and inspects
// PAGE_SIZE, overlay runtimecfg sizes, and empty overlay link metadata.
// The raw command output is always returned so callers can print it.
func CheckOverlayRuntimecfg(parentCtx context.Context, host, user, sshKeyPath string) (string, error) {
	klog.V(tsparams.LogLevel).Infof("Checking overlay storage after preinstall on %s", host)

	output, err := SSHExecBashScript(parentCtx, host, user, sshKeyPath, overlayPostPreinstallCheckScript)
	if err != nil {
		return output, fmt.Errorf("failed to inspect overlay storage on %s: %w", host, err)
	}

	err = validateOverlayRuntimecfgOutput(output)
	if err != nil {
		return output, err
	}

	return output, nil
}

// validateOverlayRuntimecfgOutput requires at least one overlay runtimecfg,
// rejects copies whose size is exactly 16MiB, and rejects empty overlay link
// files when layers exist (primary helix95 failure mode).
func validateOverlayRuntimecfgOutput(output string) error {
	var (
		foundRuntimecfg bool
		truncated       []string
		layers          int
		emptyLink       int
	)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" ||
			strings.HasPrefix(line, "PAGE_SIZE=") ||
			strings.HasPrefix(line, "searching ") ||
			strings.HasPrefix(line, "active") ||
			strings.HasPrefix(line, "inactive") ||
			strings.HasPrefix(line, "activating") ||
			strings.HasPrefix(line, "failed") ||
			strings.HasPrefix(line, "zero_size_link_lower=") {
			if strings.HasPrefix(line, "zero_size_link_lower=") {
				countStr := strings.TrimPrefix(line, "zero_size_link_lower=")
				count, err := strconv.Atoi(strings.TrimSpace(countStr))
				if err == nil && count > 0 {
					return fmt.Errorf("found %d zero-byte overlay link/lower files after preinstall", count)
				}
			}

			continue
		}

		if m := overlayLayerStatsRE.FindStringSubmatch(line); len(m) == 3 {
			layers, _ = strconv.Atoi(m[1])
			emptyLink, _ = strconv.Atoi(m[2])

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

		foundRuntimecfg = true

		if size == truncatedOverlayFileSize {
			truncated = append(truncated, line)
		}
	}

	if layers > 0 && emptyLink > 0 {
		return fmt.Errorf("overlay has %d empty link files out of %d layers after preinstall", emptyLink, layers)
	}

	if !foundRuntimecfg {
		return fmt.Errorf("no overlay runtimecfg found after preinstall")
	}

	if len(truncated) > 0 {
		return fmt.Errorf("overlay runtimecfg truncated at 16MiB: %s", strings.Join(truncated, "; "))
	}

	return nil
}
