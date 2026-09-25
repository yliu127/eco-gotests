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

	// overlayPostPreinstallCheckScript runs as root (sudo bash -s). Overlay and
	// podman storage under /var are not visible to the core SSH user.
	overlayPostPreinstallCheckScript = `set +e
echo "PAGE_SIZE=$(getconf PAGE_SIZE)"
echo "machine=$(uname -m)"
systemctl is-active install-rhcos-and-restore-seed.service 2>/dev/null || true
python3 << 'PY'
import os

overlay_roots = [
    "/var/lib/containers/storage/overlay",
    "/mnt/var/lib/containers/storage/overlay",
    "/mnt/sysroot/var/lib/containers/storage/overlay",
    "/sysroot/var/lib/containers/storage/overlay",
]

for root in overlay_roots:
    if not os.path.isdir(root):
        continue
    print(f"searching {root}")
    for dirpath, _, filenames in os.walk(root):
        if "runtimecfg" not in filenames:
            continue
        path = os.path.join(dirpath, "runtimecfg")
        try:
            size = os.path.getsize(path)
        except OSError:
            continue
        print(f"{size} {path}")

def layer_stats(root):
    if not os.path.isdir(root):
        return None
    layers = empty_link = empty_lower = 0
    try:
        names = os.listdir(root)
    except OSError:
        return None
    for name in names:
        if len(name) != 64:
            continue
        layers += 1
        link = os.path.join(root, name, "link")
        if os.path.isfile(link) and os.path.getsize(link) == 0:
            empty_link += 1
        lower = os.path.join(root, name, "lower")
        if os.path.isfile(lower) and os.path.getsize(lower) == 0:
            empty_lower += 1
    return layers, empty_link, empty_lower

stats_root = None
for root in overlay_roots:
    stats = layer_stats(root)
    if stats and stats[0] > 0:
        stats_root = root
        layers, empty_link, empty_lower = stats
        print(f"overlay_root={root}")
        print(f"layers={layers} empty_link={empty_link} empty_lower={empty_lower}")
        zero_paths = []
        for name in os.listdir(root):
            if len(name) != 64:
                continue
            for sub in ("link", "lower"):
                path = os.path.join(root, name, sub)
                if os.path.isfile(path) and os.path.getsize(path) == 0:
                    zero_paths.append(path)
        print(f"zero_size_link_lower={len(zero_paths)}")
        for path in zero_paths[:5]:
            print(f"0 {path}")
        break

if stats_root is None:
    print("overlay_root=")
    print("layers=0 empty_link=0 empty_lower=0")
    print("zero_size_link_lower=0")
PY
`
)

var overlayLayerStatsRE = regexp.MustCompile(`^layers=(\d+) empty_link=(\d+) empty_lower=(\d+)$`)

// CheckOverlayRuntimecfg SSHes to the spoke after restore-seed and inspects
// PAGE_SIZE, overlay runtimecfg sizes, and empty overlay link/lower metadata.
// The raw command output is always returned so callers can print it.
func CheckOverlayRuntimecfg(parentCtx context.Context, host, user, sshKeyPath string) (string, error) {
	klog.V(tsparams.LogLevel).Infof("Checking overlay storage after preinstall on %s", host)

	output, err := SSHExecRootBashScript(parentCtx, host, user, sshKeyPath, overlayPostPreinstallCheckScript)
	if err != nil {
		return output, fmt.Errorf("failed to inspect overlay storage on %s: %w", host, err)
	}

	err = validateOverlayRuntimecfgOutput(output)
	if err != nil {
		return output, err
	}

	return output, nil
}

// validateOverlayRuntimecfgOutput requires overlay layers, non-empty link/lower
// metadata, at least one runtimecfg, and rejects 16MiB runtimecfg copies.
func validateOverlayRuntimecfgOutput(output string) error {
	var (
		foundRuntimecfg bool
		truncated       []string
		layers          int
		emptyLink       int
		emptyLower      int
	)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" ||
			strings.HasPrefix(line, "PAGE_SIZE=") ||
			strings.HasPrefix(line, "searching ") ||
			strings.HasPrefix(line, "overlay_root=") ||
			strings.HasPrefix(line, "machine=") ||
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

		if m := overlayLayerStatsRE.FindStringSubmatch(line); len(m) == 4 {
			layers, _ = strconv.Atoi(m[1])
			emptyLink, _ = strconv.Atoi(m[2])
			emptyLower, _ = strconv.Atoi(m[3])

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

	if layers == 0 {
		return fmt.Errorf("no container overlay layers found after preinstall")
	}

	if emptyLink > 0 {
		return fmt.Errorf("overlay has %d empty link files out of %d layers after preinstall", emptyLink, layers)
	}

	if emptyLower > 0 {
		return fmt.Errorf("overlay has %d empty lower files out of %d layers after preinstall", emptyLower, layers)
	}

	if !foundRuntimecfg {
		return fmt.Errorf("no overlay runtimecfg found after preinstall")
	}

	if len(truncated) > 0 {
		return fmt.Errorf("overlay runtimecfg truncated at 16MiB: %s", strings.Join(truncated, "; "))
	}

	return nil
}
