//go:build unit_test

package iface

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateUpstreamIfaceName(t *testing.T) {
	t.Parallel()

	assert.NoError(t, validateUpstreamIfaceName("ens5f0np0"))
	assert.NoError(t, validateUpstreamIfaceName("ens5f0np0.140"))
	assert.Error(t, validateUpstreamIfaceName(""))
	assert.Error(t, validateUpstreamIfaceName("ens5f0np0;rm"))
	assert.Error(t, validateUpstreamIfaceName("ens5f0 np0"))
	assert.Error(t, validateUpstreamIfaceName(Iface(strings.Repeat("a", 16))))
}

func TestUpstreamXDPCommand(t *testing.T) {
	t.Parallel()

	command := upstreamXDPCommand("block", "ens5f0np0")

	assert.Contains(t, command, `python3 - "block" "ens5f0np0" "/sys/fs/bpf/ptp_upstream_loss_ens5f0np0"`)
	assert.Contains(t, command, "XDP_FLAGS_SKB_MODE = 2")
	assert.Contains(t, command, "0xF788")
	require.Equal(t, 2, strings.Count(command, xdpHeredocMarker))
}
