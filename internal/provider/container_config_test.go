package provider

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func configFixture(t *testing.T) (*containerConfig, *[][]string, map[string]string) {
	t.Helper()
	row := map[string]string{"registry-url": "", "username": "", "tmpdir": "", "layer-dir": ""}
	var commands [][]string
	r := &containerConfig{client: runFunc(func(_ context.Context, command ...string) ([]map[string]string, string, error) {
		commands = append(commands, command)
		switch command[0] {
		case "/container/config/print":
			if strings.Contains(command[1], "password") {
				t.Fatal("the registry password must never be read")
			}
			copied := map[string]string{}
			for key, value := range row {
				copied[key] = value
			}
			return []map[string]string{copied}, "", nil
		case "/container/config/set":
			for key, value := range words(command) {
				if key != "password" {
					row[key] = value
				}
			}
			return nil, "", nil
		}
		t.Fatalf("unexpected command %v", command)
		return nil, "", nil
	})}
	return r, &commands, row
}

func passwordSends(commands [][]string) int {
	count := 0
	for _, command := range commands {
		if slices.ContainsFunc(command, func(word string) bool { return strings.HasPrefix(word, "=password=") }) {
			count++
		}
	}
	return count
}

func TestContainerConfigSendsWriteOnlyPasswordOnlyWhenVersionChanges(t *testing.T) {
	r, commands, row := configFixture(t)
	values := map[string]string{"registry_url": "https://registry.example.net", "username": "reader", "tmpdir": "tmpfs1/pull", "password_wo": "secret-token", "password_wo_version": "1"}
	plan := resourcePlan(t, r, values)
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan, Config: tfsdk.Config(plan)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	if passwordSends(*commands) != 1 || row["registry-url"] != "https://registry.example.net" || row["tmpdir"] != "tmpfs1/pull" {
		t.Fatalf("unexpected writes: %v", *commands)
	}
	if got := stateValue(t, created.State, "password_wo"); got != "<null>" {
		t.Fatal("the password was stored in state")
	}

	// Same version: nothing is resent, even though the write-only value is present in config.
	updated := resource.UpdateResponse{State: created.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: plan, Config: tfsdk.Config(plan), State: created.State}, &updated)
	if passwordSends(*commands) != 1 {
		t.Fatal("password resent without a version change")
	}

	values["password_wo_version"] = "2"
	rotated := resourcePlan(t, r, values)
	updated = resource.UpdateResponse{State: created.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: rotated, Config: tfsdk.Config(rotated), State: created.State}, &updated)
	if updated.Diagnostics.HasError() || passwordSends(*commands) != 2 {
		t.Fatalf("rotated password was not sent: %v", updated.Diagnostics)
	}
	if got := stateValue(t, updated.State, "password_wo_version"); got != "2" {
		t.Fatalf("version = %s", got)
	}
}

func TestContainerConfigLeavesOmittedFieldsAndRouterOnDestroy(t *testing.T) {
	r, commands, row := configFixture(t)
	row["layer-dir"] = "usb1/layers"
	plan := resourcePlan(t, r, map[string]string{"registry_url": "https://registry.example.net"})
	raw := map[string]tftypes.Value{}
	if err := plan.Raw.As(&raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"username", "tmpdir", "layer_dir"} {
		raw[name] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	}
	plan.Raw = tftypes.NewValue(plan.Raw.Type(), raw)
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan, Config: tfsdk.Config(plan)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	set := (*commands)[1]
	if len(set) != 2 || set[1] != "=registry-url=https://registry.example.net" {
		t.Fatalf("unmanaged fields were written: %v", set)
	}
	if got := stateValue(t, created.State, "layer_dir"); got != "usb1/layers" {
		t.Fatalf("layer_dir = %s", got)
	}
	before := len(*commands)
	deleted := resource.DeleteResponse{}
	r.Delete(t.Context(), resource.DeleteRequest{State: created.State}, &deleted)
	if len(*commands) != before || deleted.Diagnostics.WarningsCount() != 1 {
		t.Fatal("destroy must only warn and leave the router untouched")
	}
}

func TestContainerConfigRequiresPasswordVersion(t *testing.T) {
	r := &containerConfig{}
	plan := resourcePlan(t, r, map[string]string{"password_wo": "secret"})
	resp := resource.ValidateConfigResponse{}
	r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(plan)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("password_wo without password_wo_version must be rejected")
	}
}

func TestVethValidation(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
		valid              bool
	}{
		{"interface_veth", "address", "192.168.88.20/24", true},
		{"interface_veth", "address", "192.168.88.20", false},
		{"interface_veth", "gateway", "192.168.88.1", true},
		{"interface_veth", "gateway", "router", false},
	} {
		r := managedObject{definition: testDefinition(t, tc.name)}
		var diagnostics diag.Diagnostics
		r.validateValues(map[string]string{tc.field: tc.value}, nil, &diagnostics)
		if diagnostics.HasError() == tc.valid {
			t.Fatalf("%s.%s=%q: valid=%v, diagnostics %v", tc.name, tc.field, tc.value, tc.valid, diagnostics)
		}
	}
}

func emptyState(plan tfsdk.Plan) tfsdk.State {
	return tfsdk.State{Schema: plan.Schema, Raw: tftypes.NewValue(plan.Raw.Type(), nil)}
}
