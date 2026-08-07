package tsparams

import (
	bmhv1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"github.com/openshift-kni/k8sreporter"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/cnf/ran/internal/ranparam"
)

var (
	// Labels represents the range of labels that can be used for test cases selection.
	Labels = []string{ranparam.Label, LabelSuite}

	// ReporterNamespacesToDump tells the reporter from where to collect logs on failure.
	// openshift-machine-api: Ironic/Metal3 pod logs, provisioning events, pod status.
	// The k8sreporter automatically dumps pod specs, pod logs (current+previous),
	// events, and node status for all namespaces listed here.
	ReporterNamespacesToDump = map[string]string{
		"openshift-machine-api": "machine-api",
	}

	// ReporterCRDsToDump lists additional custom resources to dump on failure.
	// - BareMetalHostList: provisioning state, conditions, and error messages.
	// - PreprovisioningImageList: Ironic image-management state for virtual-media boot.
	ReporterCRDsToDump = []k8sreporter.CRData{
		{Cr: &bmhv1alpha1.BareMetalHostList{}},
		{Cr: &bmhv1alpha1.PreprovisioningImageList{}},
	}
)
