package helpers

import (
	"fmt"
	"strings"

	siteconfigv1alpha1 "github.com/rh-ecosystem-edge/eco-goinfra/pkg/schemes/siteconfig/v1alpha1"
	"sigs.k8s.io/yaml"
)

// ClusterInstanceInstallInput holds node fields extracted from a ClusterInstance
// that are needed for BMH provisioning and IBI config generation.
type ClusterInstanceInstallInput struct {
	ClusterName    string
	HostName       string
	BMCAddress     string
	BootMACAddress string

	// NetworkConfig is the raw NMState YAML from nodeNetwork.config,
	// converted to a YAML string for the IBI config template.
	// Empty if nodeNetwork is not specified (host uses DHCP).
	NetworkConfig string
}

// PreinstallBMHName returns the BareMetalHost name for the preinstall workflow,
// derived from the node's FQDN.
func (c *ClusterInstanceInstallInput) PreinstallBMHName() string {
	return c.HostName + "-preinstall"
}

// PreinstallBMCSecretName returns the BMC secret name for the preinstall workflow.
func (c *ClusterInstanceInstallInput) PreinstallBMCSecretName() string {
	return c.ClusterName + "-preinstall-bmc-secret"
}

// ParseClusterInstance parses YAML (single or multi-doc) and returns the ClusterInstance CR.
func ParseClusterInstance(yamlData []byte) (*siteconfigv1alpha1.ClusterInstance, error) {
	docs := strings.Split(string(yamlData), "---")

	var lastUnmarshalErr error

	for _, doc := range docs {
		if strings.TrimSpace(doc) == "" {
			continue
		}

		var clusterInst siteconfigv1alpha1.ClusterInstance

		err := yaml.Unmarshal([]byte(doc), &clusterInst)
		if err != nil {
			lastUnmarshalErr = err

			continue
		}

		if clusterInst.Kind == "ClusterInstance" {
			return &clusterInst, nil
		}
	}

	if lastUnmarshalErr != nil {
		return nil, fmt.Errorf("ClusterInstance not found in YAML data (last unmarshal error: %w)", lastUnmarshalErr)
	}

	return nil, fmt.Errorf("ClusterInstance not found in YAML data")
}

// ClusterInstanceInstallInputFrom extracts BMH-related fields from the first node
// of a ClusterInstance. The image-based-installation-config fields (networkConfig,
// installationDisk, etc.) are now managed in the config template, not extracted here.
func ClusterInstanceInstallInputFrom(
	clusterInst *siteconfigv1alpha1.ClusterInstance,
) (*ClusterInstanceInstallInput, error) {
	if clusterInst == nil {
		return nil, fmt.Errorf("clusterinstance: nil")
	}

	nodes := clusterInst.Spec.Nodes
	if len(nodes) == 0 {
		return nil, fmt.Errorf("clusterinstance: spec.nodes empty")
	}

	node := &nodes[0]

	if node.HostName == "" {
		return nil, fmt.Errorf("clusterinstance: nodes[0].hostName missing or empty")
	}

	if node.BmcAddress == "" {
		return nil, fmt.Errorf("clusterinstance: nodes[0].bmcAddress missing or empty")
	}

	if node.BootMACAddress == "" {
		return nil, fmt.Errorf("clusterinstance: nodes[0].bootMACAddress missing or empty")
	}

	clusterName := clusterInst.Spec.ClusterName
	if clusterName == "" {
		return nil, fmt.Errorf("clusterinstance: spec.clusterName missing or empty")
	}

	input := &ClusterInstanceInstallInput{
		ClusterName:    clusterName,
		HostName:       node.HostName,
		BMCAddress:     node.BmcAddress,
		BootMACAddress: node.BootMACAddress,
	}

	if node.NodeNetwork != nil && len(node.NodeNetwork.NetConfig.Raw) > 0 {
		yamlBytes, err := yaml.JSONToYAML(node.NodeNetwork.NetConfig.Raw)
		if err != nil {
			return nil, fmt.Errorf("clusterinstance: failed to convert networkConfig JSON to YAML: %w", err)
		}

		input.NetworkConfig = string(yamlBytes)
	}

	return input, nil
}
