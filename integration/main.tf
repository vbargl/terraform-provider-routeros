variable "address" {
  type = string
}

variable "ca_cert_file" {
  type = string
}

variable "comment" {
  type    = string
  default = "CHR acceptance"
}

provider "routeros" {
  address      = var.address
  ca_cert_file = var.ca_cert_file
}

resource "routeros_wireguard_interface" "test" {
  name        = "wg-tf-acceptance"
  listen_port = "54231"
  mtu         = "1420"
  disabled    = "yes"
  comment     = var.comment
}

output "interface_id" {
  value = routeros_wireguard_interface.test.id
}

output "public_key" {
  value = routeros_wireguard_interface.test.public_key
}
