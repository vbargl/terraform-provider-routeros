---
page_title: "routeros_device_mode (Data Source)"
description: "Observe the RouterOS device mode."
---

# routeros_device_mode (Data Source)

```hcl
data "routeros_device_mode" "current" {}

resource "terraform_data" "container_host" {
  lifecycle {
    precondition {
      condition     = data.routeros_device_mode.current.container
      error_message = "Enable container mode in /system/device-mode and confirm it on the router."
    }
  }
}
```

The result has `mode` (for example `advanced`), `container` and `flagged`. It reflects the last refresh and needs only the `read` policy.

The provider does not change device mode. RouterOS requires physical confirmation of a device-mode change, by pressing the reset or mode button or by a cold reboot, so it cannot be completed over the API. Run `/system/device-mode/update` on the router and confirm it there.
