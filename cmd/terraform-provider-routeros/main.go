package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/vbargl/terraform-provider-routeros/internal/provider"
)

var version = "dev"

func main() {
	if err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/vbargl/routeros",
	}); err != nil {
		log.Fatal(err)
	}
}
