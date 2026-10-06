package iface

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/clients"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/cnf/ran/ptp/internal/ptpdaemon"
)

const xdpHeredocMarker = "PTP_UPSTREAM_XDP"

// upstreamIfaceNamePattern matches a Linux interface name passed to the host shell.
// A matching string looks like ens5f0np0 or ens5f0np0.140.
var upstreamIfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)

// upstreamXDPScript drops untagged L2 PTP (ethertype 0x88f7) with generic XDP and pins the
// bpf link under /sys/fs/bpf so the drop outlives this process. Generic (skb) mode runs in the
// kernel receive path, so it does not require NIC-driver XDP support. nft and tc ingress run
// too late: ptp4l's packet socket is tapped before those hooks.
const upstreamXDPScript = `
import ctypes, os, platform, struct, subprocess, sys, time

SYS_BPF = {"x86_64": 321, "aarch64": 280, "ppc64le": 361}
BPF_PROG_LOAD = 5
BPF_OBJ_PIN = 6
BPF_LINK_CREATE = 28
BPF_PROG_TYPE_XDP = 6
BPF_XDP = 37
XDP_FLAGS_SKB_MODE = 2
XDP_DROP = 1
XDP_PASS = 2

def insn(code, dst, src, off, imm):
    return struct.pack("<BBhi", code, (src << 4) | dst, off, imm)

# Untagged ethertype 0x88f7 is dropped. Shorter frames and every other ethertype are passed.
INSNS = b"".join([
    insn(0x61, 2, 1, 0, 0),
    insn(0x61, 3, 1, 4, 0),
    insn(0xbf, 4, 2, 0, 0),
    insn(0x07, 4, 0, 0, 14),
    insn(0x2d, 4, 3, 4, 0),
    insn(0x69, 4, 2, 12, 0),
    insn(0x55, 4, 0, 2, 0xF788),
    insn(0xB7, 0, 0, 0, XDP_DROP),
    insn(0x05, 0, 0, 1, 0),
    insn(0xB7, 0, 0, 0, XDP_PASS),
    insn(0x95, 0, 0, 0, 0),
])

libc = ctypes.CDLL(None, use_errno=True)
libc.syscall.restype = ctypes.c_long
libc.syscall.argtypes = [ctypes.c_long, ctypes.c_long, ctypes.c_void_p, ctypes.c_uint]

def bpf(cmd, attr_buf):
    ret = libc.syscall(SYS_BPF_NR, cmd, ctypes.byref(attr_buf), len(attr_buf))
    if ret < 0:
        err = ctypes.get_errno()
        raise OSError(err, os.strerror(err))
    return int(ret)

def link_details(iface):
    for ip_bin in ("/usr/sbin/ip", "/sbin/ip"):
        if os.path.exists(ip_bin):
            result = subprocess.run(
                [ip_bin, "-d", "link", "show", iface],
                capture_output=True, text=True, check=False)
            if result.returncode != 0:
                detail = result.stderr.strip() or result.stdout.strip()
                raise SystemExit("ip link show %s failed: %s" % (iface, detail))
            return result.stdout
    raise SystemExit("ip was not found on the host")

def xdp_attached(iface):
    return "xdpgeneric" in link_details(iface)

def unpin(path):
    if os.path.lexists(path):
        os.remove(path)

def load_prog(log_buf):
    insns_buf = ctypes.create_string_buffer(INSNS, len(INSNS))
    license_buf = ctypes.create_string_buffer(b"GPL\x00")
    attr = bytearray(128)
    struct.pack_into("<II", attr, 0, BPF_PROG_TYPE_XDP, len(INSNS) // 8)
    struct.pack_into("<Q", attr, 8, ctypes.addressof(insns_buf))
    struct.pack_into("<Q", attr, 16, ctypes.addressof(license_buf))
    struct.pack_into("<II", attr, 24, 1, len(log_buf))
    struct.pack_into("<Q", attr, 32, ctypes.addressof(log_buf))
    attr_buf = ctypes.create_string_buffer(bytes(attr), len(attr))
    return bpf(BPF_PROG_LOAD, attr_buf), insns_buf, license_buf

def attach_generic(prog_fd, ifindex):
    attr = bytearray(128)
    struct.pack_into("<IIII", attr, 0, prog_fd, ifindex, BPF_XDP, XDP_FLAGS_SKB_MODE)
    attr_buf = ctypes.create_string_buffer(bytes(attr), len(attr))
    return bpf(BPF_LINK_CREATE, attr_buf)

def pin_fd(fd, path):
    path_buf = ctypes.create_string_buffer(path.encode() + b"\x00")
    attr = bytearray(32)
    struct.pack_into("<Q", attr, 0, ctypes.addressof(path_buf))
    struct.pack_into("<I", attr, 8, fd)
    attr_buf = ctypes.create_string_buffer(bytes(attr), len(attr))
    bpf(BPF_OBJ_PIN, attr_buf)
    return path_buf

def block(iface, pin_path):
    unpin(pin_path)
    with open("/sys/class/net/%s/ifindex" % iface) as ifindex_file:
        ifindex = int(ifindex_file.read().strip())
    log_buf = ctypes.create_string_buffer(65535)
    prog_fd = None
    link_fd = None
    try:
        prog_fd, insns_buf, license_buf = load_prog(log_buf)
        del insns_buf, license_buf
        link_fd = attach_generic(prog_fd, ifindex)
        path_buf = pin_fd(link_fd, pin_path)
        del path_buf
    except OSError as err:
        unpin(pin_path)
        log = log_buf.value.decode(errors="replace").strip()
        message = "failed to attach generic XDP on %s: %s" % (iface, err)
        if log:
            message = "%s\n%s" % (message, log[-2000:])
        raise SystemExit(message)
    finally:
        if link_fd is not None:
            os.close(link_fd)
        if prog_fd is not None:
            os.close(prog_fd)
    if not xdp_attached(iface):
        unpin(pin_path)
        raise SystemExit("generic XDP did not attach on %s" % iface)
    print("dropped L2 PTP on %s with generic XDP" % iface)

def unblock(iface, pin_path):
    unpin(pin_path)
    for _ in range(20):
        if not xdp_attached(iface):
            print("restored L2 PTP on %s" % iface)
            return
        time.sleep(0.1)
    raise SystemExit("generic XDP is still attached on %s" % iface)

def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: ptp-upstream-xdp.py block|unblock IFACE PIN")
    action, iface, pin_path = sys.argv[1:]
    global SYS_BPF_NR
    SYS_BPF_NR = SYS_BPF.get(platform.machine())
    if SYS_BPF_NR is None:
        raise SystemExit("unsupported architecture %s" % platform.machine())
    if action == "block":
        block(iface, pin_path)
    elif action == "unblock":
        unblock(iface, pin_path)
    else:
        raise SystemExit("unknown action %s" % action)

main()
`

// DropUpstreamPTP stops ingress L2 PTP on each interface while leaving the link up.
// RestoreUpstreamPTP removes the drop. The interface name must already be the host netdev
// ptp4l listens on.
func DropUpstreamPTP(client *clients.Settings, nodeName string, ifaces []Iface) error {
	return runUpstreamXDP(client, nodeName, ifaces, "block")
}

// RestoreUpstreamPTP removes the generic XDP drop installed by DropUpstreamPTP and ensures
// each interface is administratively up.
func RestoreUpstreamPTP(client *clients.Settings, nodeName string, ifaces []Iface) error {
	if err := runUpstreamXDP(client, nodeName, ifaces, "unblock"); err != nil {
		return err
	}

	if err := SetInterfacesStatus(client, nodeName, ifaces, InterfaceStateUp); err != nil {
		return fmt.Errorf("failed to ensure interfaces are up on node %s after restoring PTP: %w", nodeName, err)
	}

	return nil
}

func runUpstreamXDP(client *clients.Settings, nodeName string, ifaces []Iface, action string) error {
	for _, ifaceName := range ifaces {
		if err := validateUpstreamIfaceName(ifaceName); err != nil {
			return err
		}

		_, err := ptpdaemon.ExecuteCommandInPtpDaemonPod(
			client, nodeName, upstreamXDPCommand(action, ifaceName),
			ptpdaemon.WithRetries(1), ptpdaemon.WithRetryOnError(true))
		if err != nil {
			return fmt.Errorf("failed to %s upstream PTP on %s on node %s: %w", action, ifaceName, nodeName, err)
		}
	}

	return nil
}

func validateUpstreamIfaceName(ifaceName Iface) error {
	if !upstreamIfaceNamePattern.MatchString(string(ifaceName)) {
		return fmt.Errorf("invalid interface name %q", ifaceName)
	}

	return nil
}

func upstreamXDPPinPath(ifaceName Iface) string {
	return "/sys/fs/bpf/ptp_upstream_loss_" + string(ifaceName)
}

func upstreamXDPCommand(action string, ifaceName Iface) string {
	return fmt.Sprintf(
		"nsenter -t 1 -m -n -- python3 - %s %s %s <<'%s'\n%s\n%s\n",
		strconv.Quote(action),
		strconv.Quote(string(ifaceName)),
		strconv.Quote(upstreamXDPPinPath(ifaceName)),
		xdpHeredocMarker,
		upstreamXDPScript,
		xdpHeredocMarker,
	)
}
