---
page_title: "routeros_package (Data Source)"
description: "Observe one installed RouterOS package."
---

# routeros_package (Data Source)

```hcl
data "routeros_package" "container" {
  name = "container"
}

resource "terraform_data" "container_host" {
  lifecycle {
    precondition {
      condition     = data.routeros_package.container.installed && !data.routeros_package.container.disabled
      error_message = "Install and enable the RouterOS container package."
    }
  }
}
```

`name` is required. The result has `installed`, `disabled`, `version` and `scheduled`. A package that is not installed reads as `installed = false` with an empty `version`, so a configuration can report a clear error instead of a read failure. `scheduled` shows an action pending until the next reboot, such as enabling a disabled package.

The provider does not install, enable or disable packages; these take effect only after a reboot.
