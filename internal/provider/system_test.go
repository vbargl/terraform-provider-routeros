package provider

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func readDataSource(t *testing.T, source datasource.DataSource, config map[string]tftypes.Value) (datasource.ReadResponse, map[string]tftypes.Value) {
	t.Helper()
	var schema datasource.SchemaResponse
	source.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	objectType := schema.Schema.Type().TerraformType(t.Context()).(tftypes.Object)
	values := map[string]tftypes.Value{}
	for name, attributeType := range objectType.AttributeTypes {
		values[name] = tftypes.NewValue(attributeType, nil)
	}
	for name, value := range config {
		values[name] = value
	}
	raw := tftypes.NewValue(objectType, values)
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schema.Schema, Raw: tftypes.NewValue(objectType, nil)}}
	source.Read(t.Context(), datasource.ReadRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: raw}}, &resp)
	result := map[string]tftypes.Value{}
	if !resp.Diagnostics.HasError() {
		if err := resp.State.Raw.As(&result); err != nil {
			t.Fatal(err)
		}
	}
	return resp, result
}

func boolValue(t *testing.T, value tftypes.Value) bool {
	t.Helper()
	var result bool
	if err := value.As(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func stringValue(t *testing.T, value tftypes.Value) string {
	t.Helper()
	var result string
	if err := value.As(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDeviceModeProjectsOnlyObservedFields(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"false", false}, {"no", false}, {"true", true}, {"yes", true}} {
		source := &deviceMode{client: runFunc(func(_ context.Context, words ...string) ([]map[string]string, string, error) {
			if !slices.Equal(words, []string{"/system/device-mode/print", "=.proplist=mode,container,flagged"}) {
				t.Fatalf("unexpected device-mode read: %v", words)
			}
			return []map[string]string{{"mode": "advanced", "container": tc.value, "flagged": "false"}}, "", nil
		})}
		resp, state := readDataSource(t, source, nil)
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		if boolValue(t, state["container"]) != tc.want || stringValue(t, state["mode"]) != "advanced" || boolValue(t, state["flagged"]) {
			t.Fatalf("container=%s: unexpected state %v", tc.value, state)
		}
	}
}

func TestDeviceModeRejectsUnexpectedReplies(t *testing.T) {
	for name, reply := range map[string][]map[string]string{
		"empty":         nil,
		"unknown flag":  {{"mode": "advanced", "container": "maybe", "flagged": "false"}},
		"missing mode":  {{"container": "true", "flagged": "false"}},
		"missing flags": {{"mode": "advanced"}},
	} {
		source := &deviceMode{client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
			return reply, "", nil
		})}
		if resp, _ := readDataSource(t, source, nil); !resp.Diagnostics.HasError() {
			t.Fatalf("%s: expected an error", name)
		}
	}
	denied := &deviceMode{client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
		return nil, "", errors.New("permission denied")
	})}
	if resp, _ := readDataSource(t, denied, nil); !resp.Diagnostics.HasError() {
		t.Fatal("expected a transport error")
	}
	if resp, _ := readDataSource(t, &deviceMode{}, nil); !resp.Diagnostics.HasError() {
		t.Fatal("expected an error without managed access")
	}
}

func TestPackageReportsInstalledAndDisabledState(t *testing.T) {
	name := map[string]tftypes.Value{"name": tftypes.NewValue(tftypes.String, "container")}
	for _, tc := range []struct {
		reply               []map[string]string
		installed, disabled bool
		version             string
	}{
		{[]map[string]string{{"name": "container", "version": "7.25.1", "disabled": "true", "scheduled": ""}}, true, true, "7.25.1"},
		{[]map[string]string{{"name": "container", "version": "7.25.1", "disabled": "false", "scheduled": ""}}, true, false, "7.25.1"},
		{nil, false, false, ""},
	} {
		source := &systemPackage{client: runFunc(func(_ context.Context, words ...string) ([]map[string]string, string, error) {
			if !slices.Equal(words, []string{"/system/package/print", "=.proplist=name,version,disabled,scheduled", "?name=container"}) {
				t.Fatalf("unexpected package read: %v", words)
			}
			return tc.reply, "", nil
		})}
		resp, state := readDataSource(t, source, name)
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		if boolValue(t, state["installed"]) != tc.installed || boolValue(t, state["disabled"]) != tc.disabled || stringValue(t, state["version"]) != tc.version {
			t.Fatalf("unexpected state %v for reply %v", state, tc.reply)
		}
	}
}

func TestPackageRejectsInvalidNamesAndReplies(t *testing.T) {
	called := false
	source := &systemPackage{client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
		called = true
		return nil, "", nil
	})}
	for _, name := range []string{"", "Container", "container ?name=routeros", "-x"} {
		if resp, _ := readDataSource(t, source, map[string]tftypes.Value{"name": tftypes.NewValue(tftypes.String, name)}); !resp.Diagnostics.HasError() {
			t.Fatalf("%q: expected invalid name", name)
		}
	}
	if called {
		t.Fatal("invalid names must not reach RouterOS")
	}
	for label, reply := range map[string][]map[string]string{
		"duplicate":    {{"name": "container", "disabled": "false"}, {"name": "container", "disabled": "false"}},
		"other":        {{"name": "routeros", "disabled": "false"}},
		"unknown flag": {{"name": "container", "disabled": "sometimes"}},
	} {
		source := &systemPackage{client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
			return reply, "", nil
		})}
		if resp, _ := readDataSource(t, source, map[string]tftypes.Value{"name": tftypes.NewValue(tftypes.String, "container")}); !resp.Diagnostics.HasError() {
			t.Fatalf("%s: expected an error", label)
		}
	}
}
