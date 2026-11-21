terraform {
  required_version = ">= 1.11"
  required_providers {
    routeros = {
      source = "vbargl/routeros"
    }
  }
}

provider "routeros" {
  address      = "router.example.net:8729"
  ca_cert_file = "/etc/ssl/local/router-ca.pem"
}
