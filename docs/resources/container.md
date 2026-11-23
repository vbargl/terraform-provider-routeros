---
page_title: "routeros_container (Resource)"
description: "Manage a RouterOS container."
---

# routeros_container (Resource)

```hcl
resource "routeros_container" "agent" {
  name          = "agent"
  remote_image  = "owner/agent:1.0.0"
  interface     = routeros_interface_veth.agent.name
  root_dir      = "/usb1/containers/agent"
  envlists      = [routeros_container_env_list.agent.name]
  mountlists    = [routeros_container_mount_list.agent.name]
  logging       = "yes"
  start_on_boot = "yes"
}
```

The router needs container mode in `/system/device-mode` and the `container` package enabled. Check both with the `routeros_device_mode` and `routeros_package` data sources in a precondition.

Required: `name`, `interface` and `root_dir`, plus exactly one of `remote_image` (pulled from the registry in `routeros_container_config`) and `file` (an image tar already on the router). `root_dir` is a directory on router storage, for example `/usb1/containers/agent`. RouterOS 7.25 stores it with a leading slash, so the provider treats a path with and without one leading slash as the same value and keeps your spelling in state. A different path is drift and replaces the container. `envlists` and `mountlists` are lists of `routeros_container_env_list` and `routeros_container_mount_list` names. RouterOS 7.25 accepts several names and stores them comma-separated. Names must be distinct and contain no commas or spaces. The provider keeps your order, and a reordered value read back from the router is not drift. The optional strings are `cmd`, `entrypoint`, `hostname`, `user`, `workdir` and `dns`. Omit one to leave the router's value unmanaged. `logging`, `check_certificate` and `start_on_boot` take `yes` or `no` (defaults `no`, `yes`, `no`). `restart_policy` is `no`, `on-failure` or `always` (default `no`).

Changing any setting except `comment`, `start_on_boot` and `running` replaces the container. RouterOS rejects most changes to a running container, and the intended workloads keep no state in the container.

## Env and mount lists

A container references whole lists by name. Changing the entries of a `routeros_container_env_list` or `routeros_container_mount_list` neither updates nor replaces the container.

RouterOS applies env and mount lists when the container starts, so a running container keeps its old environment and mounts until it restarts. The provider does not restart containers on list changes, because a restart interrupts the workload and should be a deliberate step. To apply a change, run `/container/restart` on the router, or let Terraform replace the container on every list change:

```hcl
lifecycle {
  replace_triggered_by = [routeros_container_env_list.agent, routeros_container_mount_list.agent]
}
```

Replacement removes the container and extracts the image again. That takes longer than a restart, especially on routers with slow storage. The start-time behaviour has not been qualified on CHR yet.

If the same key appears in several lists referenced by one container, RouterOS decides which value wins. Keep keys unique across a container's lists.

## Lifecycle

- Create adds the container and waits up to 15 minutes until the image is downloaded and extracted. If that fails, the container stays bound in state and is tainted. Check `/container/print`, free space in `root_dir` and the RouterOS log. The provider does not echo RouterOS error text, which can contain arguments.
- `running` (default `yes`) is the desired run state. A container that stopped or crashed reads as `running = "no"`, and the next apply starts it again. `state` shows the observed state: `extracting`, `starting`, `running`, `stopping`, `stopped`, `deleting` or `unknown`.
- Destroy stops the container, removes it and waits until RouterOS has deleted it.
- Import by API ID, for example `terraform import routeros_container.agent '*1'`. The imported `root_dir` is the router's spelling. If your configuration omits the leading slash, the next apply only changes the spelling in state and does not replace the container.

## Storage on small routers

Extraction needs room for the download in `tmpdir` and the extracted image in `root_dir`. On routers with little internal flash, put `root_dir` on USB storage. A tmpfs disk (`/disk add type=tmpfs tmpfs-max-size=...`) suits `tmpdir` in `routeros_container_config`. A tmpfs `root_dir` loses the extracted image on reboot. Its behaviour after a reboot has not been qualified, so prefer persistent storage for `root_dir`.

## Secrets

Do not pass secrets through `routeros_container_env_list`: values are stored in state and readable on the router. Upload secret files to the router outside Terraform and mount them read-only with `routeros_container_mount_list`.
