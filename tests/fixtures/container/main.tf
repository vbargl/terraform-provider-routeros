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

resource "routeros_container_env_list" "common" {
  name = "common"
  env  = { TZ = "Europe/Prague" }
}

resource "routeros_container_env_list" "agent" {
  name = "agent"
  env  = var.envs
}

resource "routeros_container_mount_list" "agent" {
  name  = "agent"
  mount = { for dst, src in var.mounts : dst => { src = src, mode = "ro" } }
}

resource "routeros_container" "agent" {
  name          = "agent"
  remote_image  = "example/agent:1.0.0"
  interface     = routeros_interface_veth.agent.name
  root_dir      = "usb1/agent"
  envlists      = [routeros_container_env_list.common.name, routeros_container_env_list.agent.name]
  mountlists    = [routeros_container_mount_list.agent.name]
  start_on_boot = "yes"
  depends_on    = [routeros_container_config.this]
}

output "container_id" {
  value = routeros_container.agent.id
}
