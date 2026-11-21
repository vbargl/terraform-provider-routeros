variable "address" {
  type = string
}

variable "ca_cert_file" {
  type = string
}

variable "env" {
  type = map(string)
}

variable "mount" {
  type = map(object({ src = string, mode = string }))
}

provider "routeros" {
  address      = var.address
  ca_cert_file = var.ca_cert_file
}

resource "routeros_container_env_list" "agent" {
  name = "agent"
  env  = var.env
}

resource "routeros_container_mount_list" "agent" {
  name  = "agent"
  mount = var.mount
}
