package cnfhelper

import (
	"context"
	"fmt"
	"time"

	lcav1 "github.com/openshift-kni/lifecycle-agent/api/imagebasedupgrade/v1"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/lca"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/velero"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/lca/imagebasedupgrade/cnf/internal/cnfinittools"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/lca/imagebasedupgrade/internal/safeapirequest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultSpokeIBUReadyTimeout is how long tests wait for the spoke IBU to
	// become Idle with Prep as a valid next stage.
	DefaultSpokeIBUReadyTimeout = 15 * time.Minute

	// DefaultPostRollbackIBUIdleGrace is how long to wait after auto-rollback for
	// IBU to reach Idle before falling back to abort IBGU cleanup in AfterEach.
	DefaultPostRollbackIBUIdleGrace = 10 * time.Minute

	// DefaultAbortIbguCompleteTimeout is how long to wait for an abort IBGU to
	// complete on the hub after a failed or rolled-back upgrade.
	DefaultAbortIbguCompleteTimeout = 10 * time.Minute

	oadpNamespace           = "openshift-adp"
	manualCleanupAnnotation = "lca.openshift.io/manual-cleanup-done"
)

// IsSpokeIBUIdle reports whether the spoke IBU Idle condition is True.
func IsSpokeIBUIdle(ibu *lcav1.ImageBasedUpgrade) bool {
	if ibu == nil {
		return false
	}

	for _, condition := range ibu.Status.Conditions {
		if condition.Type == string(lcav1.Stages.Idle) && condition.Status == metav1.ConditionTrue {
			return true
		}
	}

	return false
}

// IsSpokeIBUReadyForPrep reports whether the spoke IBU is Idle with Prep as a valid next stage.
func IsSpokeIBUReadyForPrep(ibu *lcav1.ImageBasedUpgrade) bool {
	if ibu == nil || ibu.Spec.Stage != lcav1.Stages.Idle {
		return false
	}

	if !IsSpokeIBUIdle(ibu) {
		return false
	}

	for _, stage := range ibu.Status.ValidNextStages {
		if stage == lcav1.Stages.Prep {
			return true
		}
	}

	return false
}

// WaitForSpokeIBUPrepReadyOptional polls until the spoke IBU is Prep-ready or gracePeriod
// elapses. It returns true when Prep-ready was observed in time.
func WaitForSpokeIBUPrepReadyOptional(gracePeriod time.Duration) (bool, error) {
	if gracePeriod <= 0 {
		gracePeriod = DefaultPostRollbackIBUIdleGrace
	}

	if cnfinittools.TargetSNOAPIClient == nil {
		return false, fmt.Errorf("target sno api client is nil")
	}

	var lastSummary string

	err := wait.PollUntilContextTimeout(
		context.TODO(), 10*time.Second, gracePeriod, true, func(_ context.Context) (bool, error) {
			ibu, err := lca.PullImageBasedUpgrade(cnfinittools.TargetSNOAPIClient)
			if err != nil {
				klog.V(100).Infof("Failed to pull spoke IBU while waiting for Idle: %v", err)

				return false, nil
			}

			lastSummary = ibuStatusSummary(ibu.Object)

			if IsSpokeIBUReadyForPrep(ibu.Object) {
				return true, nil
			}

			return false, nil
		})
	if err == nil {
		return true, nil
	}

	if wait.Interrupted(err) {
		klog.V(100).Infof("Spoke IBU not Prep-ready within %s: %s", gracePeriod, lastSummary)

		return false, nil
	}

	return false, err
}

// EnsureSpokeReadyForIbgu waits until the spoke ImageBasedUpgrade is Idle and
// Prep is a valid next stage. If leftover abort/finalize cleanup is stuck
// (Idle=False with AbortFailed or FinalizeFailed), it waits for OADP backup
// storage to become Available and sets lca.openshift.io/manual-cleanup-done so
// LCA can finish transitioning to Idle.
func EnsureSpokeReadyForIbgu(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultSpokeIBUReadyTimeout
	}

	if cnfinittools.TargetSNOAPIClient == nil {
		return fmt.Errorf("target sno api client is nil")
	}

	ibu, err := lca.PullImageBasedUpgrade(cnfinittools.TargetSNOAPIClient)
	if err != nil {
		return fmt.Errorf("failed to pull spoke IBU: %w", err)
	}

	if IsSpokeIBUReadyForPrep(ibu.Object) {
		return nil
	}

	deadline := time.Now().Add(timeout)
	remaining := func() time.Duration {
		waitFor := time.Until(deadline)
		if waitFor < time.Second {
			return 0
		}

		return waitFor
	}

	if ibuNeedsManualCleanup(ibu.Object) {
		klog.V(100).Infof("Spoke IBU requires manual cleanup before Prep: %s", ibuStatusSummary(ibu.Object))

		if remaining() == 0 {
			return fmt.Errorf("spoke IBU not ready for Prep: %s", ibuStatusSummary(ibu.Object))
		}

		if err := WaitForOADPBackupStorageAvailable(remaining()); err != nil {
			return fmt.Errorf("wait for OADP backup storage before IBU cleanup: %w", err)
		}
	}

	if remaining() == 0 {
		return fmt.Errorf("spoke IBU not ready for Prep: %s", ibuStatusSummary(ibu.Object))
	}

	var lastSummary string

	err = wait.PollUntilContextTimeout(
		context.TODO(), 10*time.Second, remaining(), true, func(_ context.Context) (bool, error) {
			ibu, err = lca.PullImageBasedUpgrade(cnfinittools.TargetSNOAPIClient)
			if err != nil {
				klog.V(100).Infof("Failed to pull spoke IBU while waiting for Prep-ready: %v", err)

				return false, nil
			}

			lastSummary = ibuStatusSummary(ibu.Object)

			if IsSpokeIBUReadyForPrep(ibu.Object) {
				return true, nil
			}

			if ibuNeedsManualCleanup(ibu.Object) {
				if err := annotateIBUManualCleanup(); err != nil {
					klog.V(100).Infof("Failed to annotate spoke IBU for manual cleanup: %v", err)
				}
			}

			klog.V(100).Infof("Spoke IBU not ready for Prep: %s", lastSummary)

			return false, nil
		})
	if err != nil {
		return fmt.Errorf("spoke IBU not ready for Prep within %s: %s: %w", timeout, lastSummary, err)
	}

	return nil
}

func ibuNeedsManualCleanup(ibu *lcav1.ImageBasedUpgrade) bool {
	if ibu == nil {
		return false
	}

	for _, condition := range ibu.Status.Conditions {
		if condition.Type == string(lcav1.Stages.Idle) && condition.Status == metav1.ConditionFalse &&
			(condition.Reason == "AbortFailed" || condition.Reason == "FinalizeFailed") {
			return true
		}
	}

	return false
}

func ibuStatusSummary(ibu *lcav1.ImageBasedUpgrade) string {
	if ibu == nil {
		return "ibu is nil"
	}

	idleReason, idleMsg := "", ""

	for _, condition := range ibu.Status.Conditions {
		if condition.Type == string(lcav1.Stages.Idle) {
			idleReason = condition.Reason
			idleMsg = condition.Message

			break
		}
	}

	return fmt.Sprintf("stage=%s idleReason=%s validNextStages=%v msg=%s",
		ibu.Spec.Stage, idleReason, ibu.Status.ValidNextStages, idleMsg)
}

// WaitForOADPBackupStorageAvailable waits until each BackupStorageLocation in
// openshift-adp reports phase Available. Abort/Idle cleanup deletes Velero
// backups and fails if the storage location is still Unavailable after reboot.
func WaitForOADPBackupStorageAvailable(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultSpokeIBUReadyTimeout
	}

	if cnfinittools.TargetSNOAPIClient == nil {
		return fmt.Errorf("target sno api client is nil")
	}

	backupStorageLocations, err := velero.ListBackupStorageLocationBuilder(
		cnfinittools.TargetSNOAPIClient,
		oadpNamespace,
		ctrlclient.ListOptions{Namespace: oadpNamespace},
	)
	if err != nil {
		return err
	}

	if len(backupStorageLocations) == 0 {
		klog.V(100).Infof("No BackupStorageLocations found in namespace %s", oadpNamespace)

		return nil
	}

	for _, backupStorageLocation := range backupStorageLocations {
		if _, err := backupStorageLocation.WaitUntilAvailable(timeout); err != nil {
			return err
		}
	}

	return nil
}

func annotateIBUManualCleanup() error {
	return safeapirequest.Do(func() error {
		ibu, err := lca.PullImageBasedUpgrade(cnfinittools.TargetSNOAPIClient)
		if err != nil {
			return err
		}

		if ibu.Definition.Annotations == nil {
			ibu.Definition.Annotations = map[string]string{}
		}

		if _, found := ibu.Definition.Annotations[manualCleanupAnnotation]; found {
			return nil
		}

		ibu.Definition.Annotations[manualCleanupAnnotation] = "true"

		_, err = ibu.Update()

		return err
	})
}
