variable "address" {
  type = string
}

provider "routeros" {
  address = var.address
}

data "routeros_device_mode" "current" {}

data "routeros_package" "container" {
  name = "container"
}

resource "terraform_data" "container_host" {
  lifecycle {
    precondition {
      condition     = data.routeros_device_mode.current.container
      error_message = "Container mode is disabled."
    }
    precondition {
      condition     = data.routeros_package.container.installed && !data.routeros_package.container.disabled
      error_message = "The container package is missing or disabled."
    }
  }
}
