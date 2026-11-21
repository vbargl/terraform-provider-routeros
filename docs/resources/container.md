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
  root_dir      = "usb1/containers/agent"
  envlists      = "agent"
  mountlists    = "agent"
  logging       = "yes"
  start_on_boot = "yes"
}
```

The router needs container mode in `/system/device-mode` and the `container` package enabled. Check both with the `routeros_device_mode` and `routeros_package` data sources in a precondition.

Required: `name`, `interface` and `root_dir`, plus exactly one of `remote_image` (pulled from the registry in `routeros_container_config`) and `file` (an image tar already on the router). The optional strings are `envlists`, `mountlists`, `cmd`, `entrypoint`, `hostname`, `user`, `workdir` and `dns`. Omit one to leave the router's value unmanaged. `logging`, `check_certificate` and `start_on_boot` take `yes` or `no` (defaults `no`, `yes`, `no`). `restart_policy` is `no`, `on-failure` or `always` (default `no`).

Changing any setting except `comment`, `start_on_boot` and `running` replaces the container. RouterOS rejects most changes to a running container, and the intended workloads keep no state in the container.

## Lifecycle

- Create adds the container and waits up to 15 minutes until the image is downloaded and extracted. If that fails, the container stays bound in state and is tainted. Check `/container/print`, free space in `root_dir` and the RouterOS log. The provider does not echo RouterOS error text, which can contain arguments.
- `running` (default `yes`) is the desired run state. A container that stopped or crashed reads as `running = "no"`, and the next apply starts it again. `state` shows the observed state: `extracting`, `starting`, `running`, `stopping`, `stopped`, `deleting` or `unknown`.
- Destroy stops the container, removes it and waits until RouterOS has deleted it.
- Import by API ID, for example `terraform import routeros_container.agent '*1'`.

## Storage on small routers

Extraction needs room for the download in `tmpdir` and the extracted image in `root_dir`. On routers with little internal flash, put `root_dir` on USB storage. A tmpfs disk (`/disk add type=tmpfs tmpfs-max-size=...`) suits `tmpdir` in `routeros_container_config`. A tmpfs `root_dir` loses the extracted image on reboot. Its behaviour after a reboot has not been qualified, so prefer persistent storage for `root_dir`.

## Secrets

Do not pass secrets through `routeros_container_env`: values are stored in state. Upload secret files to the router outside Terraform and mount them read-only with `routeros_container_mount`.
