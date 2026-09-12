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
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg`,
			wantNoErr: true,
		},
		{
			name: "truncated at 16MiB",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
16777216 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg`,
			wantErr: "overlay runtimecfg truncated at 16MiB",
		},
		{
			name: "page size only",
			output: `PAGE_SIZE=65536
`,
			wantErr: "no overlay runtimecfg found after preinstall",
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: "no overlay runtimecfg found after preinstall",
		},
		{
			name: "one healthy and one truncated",
			output: `PAGE_SIZE=65536
searching /var/lib/containers/storage/overlay
54354608 /var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg
16777216 /mnt/var/lib/containers/storage/overlay/def/diff/usr/bin/runtimecfg`,
			wantErr: "overlay runtimecfg truncated at 16MiB",
		},
		{
			name: "found under live ISO mount",
			output: `PAGE_SIZE=65536
searching /mnt/var/lib/containers/storage/overlay
51380224 /mnt/var/lib/containers/storage/overlay/abc/diff/usr/bin/runtimecfg`,
			wantNoErr: true,
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
