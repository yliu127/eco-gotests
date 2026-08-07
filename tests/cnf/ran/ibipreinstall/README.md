# IBI CNF RAN Preinstall

End-to-end test for disconnected Image Based Install (IBI) preinstall workflow.

## Overview

This suite orchestrates the full preinstall lifecycle:
1. Validates all data sources (env vars, hub API, remote YAML configs)
2. Composes `image-based-installation-config.yaml` from multiple data sources
3. Generates the IBI live installation ISO via pre-staged `openshift-install`
4. Copies ISO to the HTTP server
5. Creates BareMetalHost on the preinstall hub to boot the target
6. Waits for the `install-rhcos-and-restore-seed` service to complete

## Relationship to IBBF

This test is the **day-0** counterpart to the IBBF (Image-Based Break/Fix) test in
`tests/cnf/ran/gitopsztp/tests/IBBF-e2e-test.go`. Both operate on the same hub and
bare-metal spokes, sharing `ECO_CNF_RAN_KUBECONFIG_HUB` and `ECO_CNF_RAN_BMC_*` credentials.

- **IBI preinstall** (this suite): Prepares a bare-metal node from scratch with the seed image
- **IBBF**: Reinstalls/replaces an already-deployed cluster via SiteConfig Operator

## Data Sources

| Source | Purpose |
|--------|---------|
| **Environment Variables** | seedImage, seedVersion, binary paths, HTTP server, SSH key |
| **Hub API** | pull-secret, SSH key, CA bundle, MCE/ACM mirror locations (from IDMS/ICSP) |
| **ClusterInstance** (fetched via URL) | hostName, bmcAddress, bootMACAddress, nodeNetwork (optional) |
| **Preinstall Config** (fetched via URL) | installationDisk, ignitionConfigOverride, imageDigestSources |

## Environment Variables

### Shared with other cnf/ran suites

| Variable | Purpose |
|----------|---------|
| `ECO_CNF_RAN_KUBECONFIG_HUB` | Path to hub kubeconfig |
| `ECO_CNF_RAN_BMC_USERNAME` | BMC username |
| `ECO_CNF_RAN_BMC_PASSWORD` | BMC password |

### IBI preinstall specific

| Variable | Required | Purpose |
|----------|----------|---------|
| `ECO_CNF_RAN_IBI_SEED_IMAGE` | Yes | Seed image reference |
| `ECO_CNF_RAN_IBI_SEED_VERSION` | No | Explicit seed version override (needed if digest-pinned) |
| `ECO_CNF_RAN_IBI_CLUSTER_INSTANCE_URL` | Yes | Raw URL to ClusterInstance YAML |
| `ECO_CNF_RAN_IBI_PREINSTALL_CONFIG_URL` | No | Override URL for preinstall config |
| `ECO_CNF_RAN_IBI_OPENSHIFT_INSTALL` | Yes | Path to `openshift-install` binary |
| `ECO_CNF_RAN_IBI_PREINSTALL_HTTP_SERVER` | Yes | `user@host:/dir` (SCP destination) |
| `ECO_CNF_RAN_IBI_PREINSTALL_HTTP_BASE_URL` | Yes | HTTP URL mapping to the dir above |
| `ECO_CNF_RAN_IBI_PREINSTALL_SSH_KEY` | Yes | Path to SSH private key |

## Internal Hardcodes

- SSH user for target node journalctl polling: `core`
- Preinstall wait timeout: 60 minutes
- ISO filename: `rhcos-ibi.iso`

## Preinstall Config Format (`preinstall/<cluster>.yaml`)

```yaml
installationDisk: "/dev/disk/by-path/pci-0000:65:00.0-scsi-0:3:111:0"
ignitionConfigOverride: '{"ignition":{"version":"3.2.0"},...}'
imageDigestSources:
- source: "quay.io/openshift-release-dev/ocp-release"
  mirrors:
  - "registry.example.com/openshift/release-images"
- source: "registry.redhat.io/multicluster-engine"
  mirrors:
  - "{{.MCEMirror}}"
- source: "registry.redhat.io/rhacm2"
  mirrors:
  - "{{.ACMMirror}}"
```

## Running

```bash
ginkgo --label-filter="ibi-preinstall-end-to-end" ./tests/cnf/ran/ibipreinstall/...
```
