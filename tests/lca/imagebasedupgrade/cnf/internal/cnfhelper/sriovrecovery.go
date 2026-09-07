package cnfhelper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/sriov"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/internal/cluster"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/lca/imagebasedupgrade/cnf/internal/cnfinittools"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

const (
	sriovPostRollbackSettlePeriod = 5 * time.Minute
	sriovPostRebootRecoverTimeout = 45 * time.Minute
	sriovSyncSucceeded            = "Succeeded"
)

// RecoverSpokeSriovAfterStaterootRollback waits for SR-IOV to stay synced after an
// IBU stateroot rollback. The operator can report Succeeded briefly and then fail
// Mellanox firmware apply asynchronously; if sync is not stable, soft-reboot via MCD.
func RecoverSpokeSriovAfterStaterootRollback(timeout time.Duration) error {
	return recoverSpokeSriovIfNeeded(timeout, sriovPostRollbackSettlePeriod)
}

// IsSpokeSriovStablySynced reports whether all SriovNetworkNodeState resources report
// syncStatus Succeeded continuously for settlePeriod. When settlePeriod is 0, only the
// current sync status is checked.
func IsSpokeSriovStablySynced(settlePeriod time.Duration) (bool, error) {
	if cnfinittools.TargetSNOAPIClient == nil {
		return false, fmt.Errorf("target sno api client is nil")
	}

	operatorNamespace := cnfinittools.CNFConfig.SriovOperatorNamespace
	if operatorNamespace == "" {
		klog.V(100).Infof("SR-IOV operator namespace is empty; skipping SR-IOV sync check")

		return true, nil
	}

	nodeStates, err := sriov.ListNetworkNodeState(cnfinittools.TargetSNOAPIClient, operatorNamespace)
	if err != nil {
		return false, fmt.Errorf("list SriovNetworkNodeState: %w", err)
	}

	if len(nodeStates) == 0 {
		klog.V(100).Infof("No SriovNetworkNodeState resources found; skipping SR-IOV sync check")

		return true, nil
	}

	for _, nodeState := range nodeStates {
		if err := waitForSriovSyncStable(nodeState, settlePeriod); err != nil {
			return false, nil
		}
	}

	return true, nil
}

func recoverSpokeSriovIfNeeded(timeout, settlePeriod time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultSpokeIBUReadyTimeout
	}

	if cnfinittools.TargetSNOAPIClient == nil {
		return fmt.Errorf("target sno api client is nil")
	}

	operatorNamespace := cnfinittools.CNFConfig.SriovOperatorNamespace
	if operatorNamespace == "" {
		klog.V(100).Infof("SR-IOV operator namespace is empty; skipping SR-IOV recovery")

		return nil
	}

	nodeStates, err := sriov.ListNetworkNodeState(cnfinittools.TargetSNOAPIClient, operatorNamespace)
	if err != nil {
		return fmt.Errorf("list SriovNetworkNodeState: %w", err)
	}

	if len(nodeStates) == 0 {
		klog.V(100).Infof("No SriovNetworkNodeState resources found; skipping SR-IOV recovery")

		return nil
	}

	for _, nodeState := range nodeStates {
		if err := recoverNetworkNodeStateIfStuck(nodeState, timeout, settlePeriod); err != nil {
			return err
		}
	}

	return nil
}

func waitForSriovSyncStable(nodeState *sriov.NetworkNodeStateBuilder, settlePeriod time.Duration) error {
	if nodeState == nil {
		return nil
	}

	if settlePeriod <= 0 {
		if err := nodeState.Discover(); err != nil {
			return fmt.Errorf("discover SriovNetworkNodeState %s: %w", nodeState.Objects.Name, err)
		}

		if nodeState.Objects.Status.SyncStatus != sriovSyncSucceeded {
			return fmt.Errorf("SriovNetworkNodeState %s syncStatus=%s",
				nodeState.Objects.Name, nodeState.Objects.Status.SyncStatus)
		}

		return nil
	}

	stableSince := time.Time{}

	err := wait.PollUntilContextTimeout(
		context.TODO(), 10*time.Second, settlePeriod+10*time.Second, true,
		func(_ context.Context) (bool, error) {
			if err := nodeState.Discover(); err != nil {
				klog.V(100).Infof("Failed to discover SriovNetworkNodeState %s while waiting for stable sync: %v",
					nodeState.Objects.Name, err)

				stableSince = time.Time{}

				return false, nil
			}

			if nodeState.Objects.Status.SyncStatus != sriovSyncSucceeded {
				stableSince = time.Time{}

				return false, fmt.Errorf("SriovNetworkNodeState %s syncStatus=%s",
					nodeState.Objects.Name, nodeState.Objects.Status.SyncStatus)
			}

			if stableSince.IsZero() {
				stableSince = time.Now()
			}

			if time.Since(stableSince) >= settlePeriod {
				return true, nil
			}

			return false, nil
		})
	if err == nil {
		return nil
	}

	if wait.Interrupted(err) {
		return fmt.Errorf("SriovNetworkNodeState %s not stably synced within %s",
			nodeState.Objects.Name, settlePeriod)
	}

	return err
}

func recoverNetworkNodeStateIfStuck(
	nodeState *sriov.NetworkNodeStateBuilder, timeout, settlePeriod time.Duration,
) error {
	if nodeState == nil {
		return nil
	}

	if err := waitForSriovSyncStable(nodeState, settlePeriod); err == nil {
		return nil
	}

	klog.Infof(
		"SriovNetworkNodeState %s not stably synced after stateroot rollback; rebooting spoke via MCD",
		nodeState.Objects.Name,
	)

	if err := rebootSpokeSNO(); err != nil {
		return err
	}

	if err := cluster.WaitForRecover(cnfinittools.TargetSNOAPIClient, nil, sriovPostRebootRecoverTimeout); err != nil {
		return fmt.Errorf("wait for spoke to recover after SR-IOV reboot: %w", err)
	}

	if err := nodeState.WaitUntilSyncStatus(sriovSyncSucceeded, timeout); err != nil {
		return fmt.Errorf("SriovNetworkNodeState %s not synced after recovery reboot: %w", nodeState.Objects.Name, err)
	}

	return nil
}

func rebootSpokeSNO() error {
	err := cluster.SoftRebootSNO(cnfinittools.TargetSNOAPIClient, 3, 10*time.Second)
	if err != nil && !isRebootExecDisconnectError(err) {
		return fmt.Errorf("soft reboot spoke SNO: %w", err)
	}

	return nil
}

func isRebootExecDisconnectError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "unable to upgrade connection") ||
		strings.Contains(msg, "command terminated") ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
