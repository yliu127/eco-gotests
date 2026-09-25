package helpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOverlayRuntimecfgOutput(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		output    string
		wantErr   string
		wantNoErr bool
	}{
		{
			name: "healthy arm64 runtimecfg",
			output: `PAGE_SIZE=4096
machine=aarch64
active
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
overlay_root=/var/lib/containers/storage/overlay
layers=139 empty_link=0 empty_lower=0
zero_size_link_lower=0`,
			wantNoErr: true,
		},
		{
			name: "truncated at 16MiB",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
16777216 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
layers=139 empty_link=0 empty_lower=0`,
			wantErr: "overlay runtimecfg truncated at 16MiB",
		},
		{
			name: "page size only",
			output: `PAGE_SIZE=65536
layers=0 empty_link=0 empty_lower=0`,
			wantErr: "no container overlay layers found",
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: "no container overlay layers found",
		},
		{
			name: "one healthy and one truncated",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
16777216 /mnt/var/lib/containers/storage/overlay/def/diff/usr/bin/runtimecfg
layers=139 empty_link=0 empty_lower=0`,
			wantErr: "overlay runtimecfg truncated at 16MiB",
		},
		{
			name: "found under live ISO mount",
			output: `PAGE_SIZE=65536
searching /mnt/var/lib/containers/storage/overlay
51380224 /mnt/var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
overlay_root=/mnt/var/lib/containers/storage/overlay
layers=120 empty_link=0 empty_lower=0
zero_size_link_lower=0`,
			wantNoErr: true,
		},
		{
			name: "empty overlay link metadata",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
layers=139 empty_link=63 empty_lower=53
zero_size_link_lower=116`,
			wantErr: "found 116 zero-byte overlay link/lower files after preinstall",
		},
		{
			name: "empty overlay lower metadata",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
layers=139 empty_link=0 empty_lower=53
zero_size_link_lower=53`,
			wantErr: "found 53 zero-byte overlay link/lower files after preinstall",
		},
		{
			name: "empty overlay link metadata without zero_size summary",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
layers=139 empty_link=2 empty_lower=0
zero_size_link_lower=0`,
			wantErr: "empty link files",
		},
		{
			name: "empty overlay lower metadata without zero_size summary",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
layers=139 empty_link=0 empty_lower=3
zero_size_link_lower=0`,
			wantErr: "empty lower files",
		},
		{
			name: "layers but missing runtimecfg",
			output: `PAGE_SIZE=65536
overlay_root=/var/lib/containers/storage/overlay
layers=139 empty_link=0 empty_lower=0
zero_size_link_lower=0`,
			wantErr: "no overlay runtimecfg found after preinstall",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateOverlayRuntimecfgOutput(testCase.output)
			if testCase.wantNoErr {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.wantErr)
		})
	}
}
