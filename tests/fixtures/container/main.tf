variable "address" {
  type = string
}

variable "registry_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

provider "routeros" {
  address = var.address
}

resource "routeros_container_config" "this" {
  registry_url        = "https://registry.example.net"
  username            = "reader"
  tmpdir              = "tmpfs1/pull"
  password_wo         = var.registry_token
  password_wo_version = "1"
}

resource "routeros_interface_veth" "agent" {
  name    = "veth-agent"
  address = "192.168.88.20/24"
  gateway = "192.168.88.1"
}

variable "envs" {
  type = map(string)
  default = {
    POD_NAMESPACE = "agents"
    POOL          = "suspendable"
    IDENTITY      = "router"
  }
}

variable "mounts" {
  type = map(string)
  default = {
    "/etc/agent"     = "/usb1/agent/kubeconfig"
    "/etc/agent-tls" = "/usb1/agent/ca"
  }
}

resource "routeros_container_env" "common" {
  list  = "common"
  key   = "TZ"
  value = "Europe/Prague"
}

resource "routeros_container_env" "agent" {
  for_each = var.envs
  list     = "agent"
  key      = each.key
  value    = each.value
}

resource "routeros_container_mount" "agent" {
  for_each = var.mounts
  list     = "agent"
  src      = each.value
  dst      = each.key
  mode     = "ro"
}

resource "routeros_container" "agent" {
  name          = "agent"
  remote_image  = "example/agent:1.0.0"
  interface     = routeros_interface_veth.agent.name
  root_dir      = "usb1/agent"
  envlists      = [routeros_container_env.common.list, "agent"]
  mountlists    = ["agent"]
  start_on_boot = "yes"
  # Entries are referenced by list name; the container does not depend on each entry.
  depends_on = [routeros_container_config.this, routeros_container_env.agent, routeros_container_mount.agent]
}

output "container_id" {
  value = routeros_container.agent.id
}
