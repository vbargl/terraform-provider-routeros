package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func configureCredentials(t *testing.T, cfg providerConfig) (*routerClient, provider.ConfigureResponse) {
	t.Helper()
	p := routerProvider{}
	var schema provider.SchemaResponse
	p.Schema(t.Context(), provider.SchemaRequest{}, &schema)
	if cfg.Address.IsNull() {
		cfg.Address = types.StringValue("router.example:8729")
	}
	config := tfsdk.State{Schema: schema.Schema}
	if diagnostics := config.Set(t.Context(), cfg); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var resp provider.ConfigureResponse
	p.Configure(t.Context(), provider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: config.Raw}}, &resp)
	if resp.Diagnostics.HasError() {
		return nil, resp
	}
	client, _ := resp.ResourceData.(*routerClient)
	if client == nil {
		client, _ = resp.EphemeralResourceData.(*routerClient)
	}
	return client, resp
}

func TestProviderCredentials(t *testing.T) {
	t.Run("arguments override the environment", func(t *testing.T) {
		t.Setenv("ROUTEROS_USERNAME", "env-user")
		t.Setenv("ROUTEROS_PASSWORD", "env-password")
		client, _ := configureCredentials(t, providerConfig{
			Username: types.StringValue("arg-user"), Password: types.StringValue("arg-password"),
		})
		if client == nil || client.username != "arg-user" || client.password != "arg-password" {
			t.Fatal("provider arguments must take precedence over the environment")
		}
	})
	t.Run("environment fills missing arguments", func(t *testing.T) {
		t.Setenv("ROUTEROS_USERNAME", "env-user")
		t.Setenv("ROUTEROS_PASSWORD", "env-password")
		client, _ := configureCredentials(t, providerConfig{Username: types.StringValue("arg-user")})
		if client == nil || client.username != "arg-user" || client.password != "env-password" {
			t.Fatal("expected the argument username and the environment password")
		}
	})
	t.Run("ephemeral access keeps its own environment", func(t *testing.T) {
		t.Setenv("ROUTEROS_USERNAME", "env-user")
		t.Setenv("ROUTEROS_PASSWORD", "env-password")
		t.Setenv("ROUTEROS_EPHEMERAL_USERNAME", "key-user")
		t.Setenv("ROUTEROS_EPHEMERAL_PASSWORD", "key-password")
		client, _ := configureCredentials(t, providerConfig{EphemeralAccess: types.BoolValue(true)})
		if client == nil || client.username != "key-user" || client.password != "key-password" {
			t.Fatal("ephemeral access must read ROUTEROS_EPHEMERAL_*")
		}
	})
	t.Run("missing credentials fail", func(t *testing.T) {
		t.Setenv("ROUTEROS_USERNAME", "")
		t.Setenv("ROUTEROS_PASSWORD", "")
		if _, resp := configureCredentials(t, providerConfig{Username: types.StringValue("arg-user")}); !resp.Diagnostics.HasError() {
			t.Fatal("expected an error without a password")
		}
	})
}
