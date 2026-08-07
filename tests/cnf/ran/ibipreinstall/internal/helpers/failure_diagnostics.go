package helpers

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/clients"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	. "github.com/rh-ecosystem-edge/eco-gotests/tests/cnf/ran/internal/raninittools"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/cnf/ran/internal/ranconfig"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/cnf/ran/ibipreinstall/internal/tsparams"
)

const diagnosticsSSHTimeout = 5 * time.Minute

// spokeJournalUnits lists the systemd units whose full journals are collected
// from the spoke node on test failure.
var spokeJournalUnits = []string{
	tsparams.PreinstallServiceUnit,
	"precache.service",
	"set-ip-address.service",
}

// CollectDiagnosticsIfFailed collects IBI preinstall diagnostics when a test
// fails.  It mirrors the pattern of the PTP suite's MustGatherIfFailed:
// self-contained, called from the suite-level JustAfterEach, and determines the
// dump directory from the global RANConfig.
func CollectDiagnosticsIfFailed(
	report types.SpecReport,
	testSuite string,
	hubClient *clients.Settings,
	spokeHost, workDir string,
) {
	if !report.State.Is(types.SpecStateFailureStates) {
		return
	}

	dumpDir := RANConfig.GetDumpFailedTestReportLocation(testSuite)
	if dumpDir == "" {
		klog.V(tsparams.LogLevel).Info("No dump directory configured, skipping IBI diagnostics")

		return
	}

	diagDir := filepath.Join(dumpDir, "ibi-preinstall-diagnostics")
	if err := os.MkdirAll(diagDir, 0o755); err != nil {
		klog.V(tsparams.LogLevel).Infof("Failed to create diagnostics dir %s: %v", diagDir, err)

		return
	}

	if spokeHost != "" {
		sshKeyPath := RANConfig.IBIPreinstallConfig.PreinstallSSHKey
		collectSpokeJournals(spokeHost, ranconfig.IBITargetNodeSSHUser, sshKeyPath, diagDir)
	}

	if workDir != "" {
		preserveArtifact(filepath.Join(workDir, ".openshift_install.log"), diagDir)
		preserveArtifact(filepath.Join(workDir, "image-based-installation-config.yaml"), diagDir)
	}

	collectHubDiagnostics(hubClient, diagDir)
}

func collectSpokeJournals(host, user, sshKeyPath, destDir string) {
	ctx, cancel := context.WithTimeout(context.TODO(), diagnosticsSSHTimeout)
	defer cancel()

	for _, unit := range spokeJournalUnits {
		cmd := fmt.Sprintf("journalctl -u %s --no-pager 2>&1 || true", unit)

		output, err := SSHExec(ctx, host, user, sshKeyPath, cmd)
		if err != nil {
			klog.V(tsparams.LogLevel).Infof(
				"Could not collect journal for %s from %s: %v", unit, host, err)

			continue
		}

		dest := filepath.Join(destDir, "spoke_journal_"+unit+".log")
		if writeErr := os.WriteFile(dest, []byte(output), 0o644); writeErr != nil {
			klog.V(tsparams.LogLevel).Infof("Failed to write journal to %s: %v", dest, writeErr)
		}
	}

	output, err := SSHExec(ctx, host, user, sshKeyPath, "dmesg --time-format iso 2>&1 | tail -500 || true")
	if err != nil {
		klog.V(tsparams.LogLevel).Infof("Could not collect dmesg from %s: %v", host, err)
	} else {
		dest := filepath.Join(destDir, "spoke_dmesg.log")
		if writeErr := os.WriteFile(dest, []byte(output), 0o644); writeErr != nil {
			klog.V(tsparams.LogLevel).Infof("Failed to write dmesg to %s: %v", dest, writeErr)
		}
	}
}

func preserveArtifact(srcPath, destDir string) {
	src, err := os.Open(srcPath)
	if err != nil {
		klog.V(tsparams.LogLevel).Infof("Artifact %s not available: %v", srcPath, err)

		return
	}

	defer src.Close()

	destPath := filepath.Join(destDir, filepath.Base(srcPath))

	dst, err := os.Create(destPath)
	if err != nil {
		klog.V(tsparams.LogLevel).Infof("Failed to create %s: %v", destPath, err)

		return
	}

	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		klog.V(tsparams.LogLevel).Infof("Failed to copy artifact to %s: %v", destPath, err)
	}
}

func collectHubDiagnostics(apiClient *clients.Settings, destDir string) {
	if apiClient == nil {
		return
	}

	ctx := context.TODO()

	idmsList, err := apiClient.ImageDigestMirrorSets().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.V(tsparams.LogLevel).Infof("Failed to list IDMS for diagnostics: %v", err)
	} else {
		data, marshalErr := yaml.Marshal(idmsList)
		if marshalErr == nil {
			_ = os.WriteFile(filepath.Join(destDir, "hub_idms_list.yaml"), data, 0o644)
		}
	}

	type resourceSummary struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}

	var summary []resourceSummary

	secrets, err := apiClient.Secrets("openshift-config").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range secrets.Items {
			summary = append(summary, resourceSummary{Kind: "Secret", Name: secrets.Items[i].Name})
		}
	}

	configMaps, err := apiClient.ConfigMaps("openshift-config").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range configMaps.Items {
			summary = append(summary, resourceSummary{Kind: "ConfigMap", Name: configMaps.Items[i].Name})
		}
	}

	if len(summary) > 0 {
		data, marshalErr := yaml.Marshal(summary)
		if marshalErr == nil {
			_ = os.WriteFile(filepath.Join(destDir, "hub_openshift_config_resources.yaml"), data, 0o644)
		}
	}
}
