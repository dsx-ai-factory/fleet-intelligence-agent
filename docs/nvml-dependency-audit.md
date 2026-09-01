# NVML Dependency Audit

> Pre-migration snapshot: this records the direct NVML dependencies and known
> compatibility gaps identified before the DCGM migration began.

## What currently depends on NVML

| Feature/signal                                   | Current NVML use                                                                                                                                                                                                                                              | Replacement                                                                                       |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| Machine inventory and telemetry identity         | Driver/CUDA versions; product, brand, architecture, memory; per-GPU UUID, serial, minor ID, board ID, PCI bus, model, fabric IDs, VBIOS, chassis serial, and index ([collector](../third_party/fleet-intelligence-sdk/pkg/machine-info/machine_info.go#L328)) | DCGM covers nearly everything                                                                     |
| `accelerator-nvidia-persistence-mode`            | Direct `GetPersistenceMode` per GPU ([component](../third_party/fleet-intelligence-sdk/components/accelerator/nvidia/persistence-mode/component.go#L140))                                                                                                     | `DCGM_FI_DEV_PERSISTENCE_MODE`                                                                    |
| `accelerator-nvidia-nvml`                        | Reports errors encountered during inventory collection                                                                                                                                                                                                        | Remove the component; inventory remains a separately monitored collection workflow                |
| XID                                              | NVML gates the component, maps PCI bus IDs to UUIDs, and decides whether XID 63/64 should be suppressed                                                                                                                                                       | DCGM UUID-to-PCI map plus existing product-capability tables                                      |
| InfiniBand, NCCL, peermem, and SXID              | NVML is only used to decide whether this is a GPU host                                                                                                                                                                                                        | DCGM device inventory; no PCI/sysfs fallback, matching the existing provider-gating behavior      |
| `library`                                        | NVML availability decides whether `libnvidia-ml.so` and `libcuda.so` are checked                                                                                                                                                                              | Gate on NVIDIA GPU/driver presence; decide whether the NVML library-presence check itself remains |
| Precheck                                         | Driver version, architecture, and GPU detail collection ([precheck](../internal/precheck/precheck.go#L106))                                                                                                                                                   | DCGM primary; `/proc/driver/nvidia`, sysfs, and PCI fallback                                      |
| `scan`, `machine-info`, and enrollment inventory | Explicitly create an NVML instance                                                                                                                                                                                                                            | Pass a neutral inventory provider/DCGM instance                                                   |
| `/machine-info`                                  | `nvidia_available` is based on `NVMLInstance != nil`                                                                                                                                                                                                          | Use DCGM devices or PCI detection                                                                 |

The daemon owns the full NVML lifecycle today: it initializes NVML, puts it in `GPUdInstance`, passes it into inventory and the exporter, and shuts it down ([server](../internal/server/server.go#L273)). If NVML is initially missing, it polls every five seconds and exits the process when NVML appears so the supervisor can restart it. DCGM already has a reconnecting instance, so that exit/restart mechanism can disappear.

One noteworthy bug: `nvidia_available` is effectively always true during a normal server run because a missing library produces a non-nil no-op NVML instance ([handler](../internal/server/handlers.go#L242)).

Runtime component gating does not independently probe PCI or sysfs. The current
NVML-backed components treat an unavailable NVML instance or missing product
name as no detected GPU. The DCGM replacement preserves that behavior: when
DCGM is unavailable or initialization times out, its reconnecting no-op
instance exposes an empty inventory, so GPU-gated components report Healthy as
unsupported while connection attempts continue in the background. A
`GetSupportedDevices` error also invalidates that candidate DCGM session: the
agent keeps the reconnecting no-op instance and retries the complete session
initialization instead of publishing an apparently connected instance with an
empty inventory. Failures that occur only while enriching successfully
enumerated device IDs retain those IDs for presence checks. Adding independent
PCI/sysfs GPU detection would be a separate reliability improvement rather
than part of NVML compatibility.

The pinned DCGM API already exposes:

- Driver and CUDA-driver versions
- GPU model, brand, UUID, serial, minor number, and PCI bus ID
- VBIOS and framebuffer memory
- Persistence mode
- Fabric cluster UUID and clique ID
- Chassis serial
- DCGM device ID for the exported `gpu` label

These are standard DCGM fields documented in the [NVIDIA DCGM field identifiers](https://docs.nvidia.com/datacenter/dcgm/latest/dcgm-api/dcgm-api-field-ids.html).

The underlying `go-dcgm.GetDeviceInfo` already returns model, brand, serial, VBIOS, driver version, memory, and PCI information. Fleetint's wrapper currently discards all of that except `ID` and `UUID`, so extending its `DeviceInfo` is the natural first move.

## Remaining compatibility gaps

1. **Board ID:** DCGM has no equivalent to `nvmlDeviceGetBoardId`. The migration keeps the field and sends `0`; exact parity is unavailable. NVML's board ID is configuration-local and not stable across reboots. See the [NVML board-ID semantics](https://docs.nvidia.com/deploy/nvml-api/group__nvmlDeviceQueries.html).

2. **Architecture:** DCGM has compute capability but no architecture-name field. The migration derives the existing lowercase architecture values from compute capability, including Ampere, Ada, Hopper, Blackwell, and Rubin. The mapping must be extended and tested as new GPU architectures appear.

3. **Persistence error detail:** Enabled, disabled, and unsupported states are preserved. The legacy NVML implementation queried persistence mode per GPU and translated `GPU_IS_LOST` and `GPU_REQUIRES_RESET` into persistence-specific reboot advice. The shared DCGM cache polls all registered fields together and currently exposes one aggregate polling error, so attributing that device-wide failure and repair action specifically to persistence mode would be misleading. Persistence mode now follows the other DCGM field components: unhealthy DCGM errors remain visible, but no persistence-specific reboot action is attached.

4. **Library check:** The `library` component continues checking `libcuda.so` on GPU hosts but intentionally no longer requires `libnvidia-ml.so`; otherwise the agent would still treat NVML as a required runtime surface.

5. **Inventory component signal:** The legacy `accelerator-nvidia-nvml` component is removed rather than translated into a DCGM health component. Inventory collection remains a separate workflow with its existing failure logging and retry behavior, but it no longer emits this component health state.

6. **SDK API compatibility:** The direct NVML query packages and their NVML-specific error/device types are removed. Out-of-tree SDK consumers importing those packages must migrate to the DCGM inventory interface.

7. **Dependency boundary:** The agent no longer imports, loads, or checks NVML directly. DCGM and the NVIDIA driver may still use NVML internally and expose DCGM status names containing `NVML`; eliminating that system-level implementation detail is outside the agent's control.

The GB10 unified-memory fallback can remain: use DCGM's model and framebuffer result, then fall back to system memory when the model ends in `GB10`.
