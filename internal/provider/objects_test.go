package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type runFunc func(context.Context, ...string) ([]map[string]string, string, error)

func (f runFunc) run(ctx context.Context, command ...string) ([]map[string]string, string, error) {
	return f(ctx, command...)
}

func testDefinition(t *testing.T, name string) definition {
	t.Helper()
	for _, object := range objects {
		if object.name == name {
			return object
		}
	}
	t.Fatalf("no resource named %s", name)
	return definition{}
}

func TestWireGuardReadRequestsOnlyPublicFields(t *testing.T) {
	r := managedObject{
		definition: testDefinition(t, "wireguard_interface"),
		client: runFunc(func(_ context.Context, command ...string) ([]map[string]string, string, error) {
			expected := []string{"/interface/wireguard/print", "=.proplist=.id,name,listen-port,mtu,comment,disabled,public-key", "?.id=*A"}
			if !slices.Equal(command, expected) {
				t.Fatalf("unexpected WireGuard read: %v", command)
			}
			return []map[string]string{{".id": "*A", "public-key": "public identity"}}, "", nil
		}),
	}
	row, err := r.read(t.Context(), "*a", "")
	if err != nil || row["public-key"] != "public identity" {
		t.Fatalf("read failed: %v", err)
	}
}

func TestReadDoesNotConfuseTransportErrorWithDeletion(t *testing.T) {
	denied := errors.New("permission denied")
	r := managedObject{definition: testDefinition(t, "wireguard_interface"), client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
		return nil, "", denied
	})}
	if _, err := r.read(t.Context(), "*A", ""); !errors.Is(err, denied) {
		t.Fatalf("expected permission failure, got %v", err)
	}
}

func TestImportRejectsDynamicLeases(t *testing.T) {
	r := managedObject{
		definition: testDefinition(t, "dhcp_lease"),
		client: runFunc(func(_ context.Context, args ...string) ([]map[string]string, string, error) {
			if !strings.Contains(args[1], ",dynamic") {
				t.Fatal("dynamic status omitted from projection")
			}
			return []map[string]string{{".id": "*A", "dynamic": "true"}}, "", nil
		}),
	}
	if _, err := r.read(t.Context(), "*A", ""); err == nil {
		t.Fatal("expected dynamic lease rejection")
	}
}

func TestImportRejectsDynamicBridgePorts(t *testing.T) {
	r := managedObject{
		definition: testDefinition(t, "interface_bridge_port"),
		client: runFunc(func(_ context.Context, args ...string) ([]map[string]string, string, error) {
			if args[0] != "/interface/bridge/port/print" || !strings.Contains(args[1], "bridge,interface,edge,comment,disabled,dynamic") {
				t.Fatalf("unexpected projection %v", args)
			}
			return []map[string]string{{".id": "*A", "bridge": "lan", "interface": "wlan1", "dynamic": "true"}}, "", nil
		}),
	}
	if _, err := r.read(t.Context(), "*A", ""); err == nil {
		t.Fatal("expected dynamic bridge port rejection")
	}
}

func objectPlan(t *testing.T, r *managedObject, values map[string]types.String) tfsdk.Plan {
	t.Helper()
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema, Raw: tftypes.NewValue(schema.Schema.Type().TerraformType(t.Context()), nil)}
	for field, value := range values {
		if diagnostics := state.SetAttribute(t.Context(), attr(field), value); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
	}
	return tfsdk.Plan{Schema: schema.Schema, Raw: state.Raw}
}

func TestConcurrentRuleCreatesLeaveUnconfiguredPositionUnmanaged(t *testing.T) {
	lock := &sync.Mutex{}
	var rows []map[string]string
	var commands []string
	client := runFunc(func(_ context.Context, words ...string) ([]map[string]string, string, error) {
		if lock.TryLock() {
			lock.Unlock()
			return nil, "", errors.New("rule operation ran outside the shared menu lock")
		}
		commands = append(commands, words[0])
		switch words[0] {
		case "/ip/firewall/filter/add":
			if slices.Contains(words, "=protocol=") || slices.Contains(words, "=place-before=") {
				return nil, "", errors.New("add sent an unmanaged empty field")
			}
			id := []string{"*A", "*B"}[len(rows)]
			rows = append(rows, map[string]string{".id": id, "chain": "forward", "action": "drop", "disabled": "true"})
			return nil, id, nil
		case "/ip/firewall/filter/print":
			return rows, "", nil
		default:
			return nil, "", errors.New("unexpected rule command")
		}
	})
	var wait sync.WaitGroup
	for range 2 {
		r := managedObject{definition: testDefinition(t, "firewall_rule"), client: client, menuLock: lock}
		plan := objectPlan(t, &r, map[string]types.String{
			"chain": types.StringValue("forward"), "action": types.StringValue("drop"),
			"src-address": types.StringValue("0.0.0.0/0"), "dst-address": types.StringValue("0.0.0.0/0"),
			"disabled": types.StringValue("yes"),
		})
		wait.Go(func() {
			result := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
			r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &result)
			var position types.String
			if result.Diagnostics.HasError() {
				t.Error(result.Diagnostics)
			} else if diagnostics := result.State.GetAttribute(t.Context(), attr("place-before"), &position); diagnostics.HasError() || !position.IsNull() {
				t.Errorf("position was adopted without configuration: %v %v", position, diagnostics)
			}
		})
	}
	wait.Wait()
	if !slices.Equal(commands, []string{"/ip/firewall/filter/add", "/ip/firewall/filter/print", "/ip/firewall/filter/add", "/ip/firewall/filter/print"}) {
		t.Errorf("rule writes and verification interleaved: %v", commands)
	}

	// After the next rule is inserted, refresh still leaves the first position null.
	r := managedObject{definition: testDefinition(t, "firewall_rule"), client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
		return rows, "", nil
	})}
	plan := objectPlan(t, &r, map[string]types.String{"id": types.StringValue("*A")})
	state := tfsdk.State(plan)
	result := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &result)
	var position types.String
	if diagnostics := result.State.GetAttribute(t.Context(), attr("place-before"), &position); result.Diagnostics.HasError() || diagnostics.HasError() || !position.IsNull() {
		t.Fatalf("refresh adopted an unmanaged position: %v %v %v", position, result.Diagnostics, diagnostics)
	}
}

func TestRuleUpdateClearsOnlyChangedFieldAndMovesBeforeAnchor(t *testing.T) {
	rows := []map[string]string{
		{".id": "*B"},
		{".id": "*A", "chain": "forward", "action": "drop", "disabled": "true", "comment": "old"},
	}
	var mutations [][]string
	r := managedObject{definition: testDefinition(t, "firewall_rule"), client: runFunc(func(_ context.Context, words ...string) ([]map[string]string, string, error) {
		switch words[0] {
		case "/ip/firewall/filter/print":
			return rows, "", nil
		case "/ip/firewall/filter/set":
			mutations = append(mutations, words)
			rows[1]["comment"] = ""
		case "/ip/firewall/filter/move":
			mutations = append(mutations, words)
			rows[0], rows[1] = rows[1], rows[0]
		}
		return nil, "", nil
	})}
	plan := objectPlan(t, &r, map[string]types.String{
		"id": types.StringValue("*A"), "chain": types.StringValue("forward"), "action": types.StringValue("drop"),
		"src-address": types.StringValue("0.0.0.0/0"), "dst-address": types.StringValue("0.0.0.0/0"),
		"disabled": types.StringValue("yes"), "place-before": types.StringValue("*b"), "comment": types.StringValue(""),
	})
	state := tfsdk.State(plan)
	result := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{Plan: plan, State: state}, &result)
	if result.Diagnostics.HasError() {
		t.Fatal(result.Diagnostics)
	}
	if len(mutations) != 2 || !slices.Equal(mutations[0], []string{"/ip/firewall/filter/set", "=.id=*A", "=comment="}) ||
		!slices.Equal(mutations[1], []string{"/ip/firewall/filter/move", "=numbers=*A", "=destination=*B"}) {
		t.Errorf("unexpected rule mutations: %v", mutations)
	}
}

func TestRouterValuesCompareEquivalentForms(t *testing.T) {
	for _, test := range []struct {
		name, field, configured, observed string
	}{
		{"wireguard_interface", "disabled", "yes", "true"},
		{"firewall_rule", "src-address", "0.0.0.0/0", ""},
		{"firewall_rule", "dst-address", "192.0.2.1/32", "192.0.2.1"},
		{"ip_route", "dst-address", "192.0.2.1/24", "192.0.2.0/24"},
		{"dhcp_lease", "mac-address", "aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF"},
		{"dhcp_server", "lease-time", "60m", "01:00:00"},
		{"dhcp_server", "lease-time", "25h", "1d1h"},
		{"dhcp_server", "lease-time", "25h", "1d 1h"},
		{"dhcp_server", "lease-time", "220h30m", "1w2d4h30m"},
		{"wireguard_peer", "persistent-keepalive", "25", "25s"},
		{"wireguard_peer", "allowed-address", "192.0.2.1, 2001:db8::1/128", "2001:db8::1,192.0.2.1/32"},
	} {
		r := managedObject{definition: definition{name: test.name}}
		if r.normalize(test.field, test.configured) != r.normalize(test.field, test.observed) {
			t.Errorf("%s %s: %q and %q did not compare equal", test.name, test.field, test.configured, test.observed)
		}
	}
}

func TestMoveBeforeEnablingAnOrderedRule(t *testing.T) {
	for _, outcome := range []string{"success", "rejected", "unverified"} {
		t.Run(outcome, func(t *testing.T) {
			own := map[string]string{".id": "*A", "chain": "forward", "action": "drop", "disabled": "true"}
			anchor := map[string]string{".id": "*B"}
			rows := []map[string]string{anchor, own}
			var calls []string
			r := managedObject{definition: testDefinition(t, "firewall_rule"), client: runFunc(func(_ context.Context, words ...string) ([]map[string]string, string, error) {
				calls = append(calls, words[0])
				switch words[0] {
				case "/ip/firewall/filter/print":
					return rows, "", nil
				case "/ip/firewall/filter/move":
					if outcome == "rejected" {
						return nil, "", errors.New("move denied")
					}
					if outcome == "success" {
						rows = []map[string]string{own, anchor}
					}
				case "/ip/firewall/filter/set":
					if !slices.Contains(words, "=disabled=no") {
						t.Fatal("expected rule activation")
					}
					own["disabled"] = "false"
				}
				return nil, "", nil
			})}
			plan := objectPlan(t, &r, map[string]types.String{
				"id": types.StringValue("*A"), "chain": types.StringValue("forward"), "action": types.StringValue("drop"),
				"src-address": types.StringValue("0.0.0.0/0"), "dst-address": types.StringValue("0.0.0.0/0"),
				"disabled": types.StringValue("no"), "place-before": types.StringValue("*B"),
			})
			state := tfsdk.State(plan)
			result := resource.UpdateResponse{State: state}
			r.Update(t.Context(), resource.UpdateRequest{Plan: plan, State: state}, &result)
			if outcome != "success" {
				if !result.Diagnostics.HasError() || slices.Contains(calls, "/ip/firewall/filter/set") {
					t.Fatalf("activation followed an unsuccessful move: %v %v", calls, result.Diagnostics)
				}
				return
			}
			expected := []string{"/ip/firewall/filter/print", "/ip/firewall/filter/move", "/ip/firewall/filter/print", "/ip/firewall/filter/set", "/ip/firewall/filter/print"}
			if result.Diagnostics.HasError() || !slices.Equal(calls, expected) {
				t.Fatalf("wrong activation order: %v %v", calls, result.Diagnostics)
			}
		})
	}
}

func TestRuleAnchorAllowsOtherRulesBetween(t *testing.T) {
	rows := []map[string]string{{".id": "*A"}, {".id": "*B"}, {".id": "*C"}}
	r := managedObject{definition: testDefinition(t, "firewall_rule"), client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
		return rows, "", nil
	})}
	for _, id := range []string{"*A", "*B"} {
		row, err := r.read(t.Context(), id, "*C")
		if err != nil || row["place-before"] != "*C" {
			t.Fatalf("rule %s did not satisfy shared anchor: %v", id, err)
		}
	}
	rows = []map[string]string{rows[1], rows[2], rows[0]}
	row, err := r.read(t.Context(), "*A", "*C")
	if err != nil || row["place-before"] != "" {
		t.Fatalf("rule after its anchor did not drift: %v %v", row, err)
	}
}

func TestValidateConfigChecksKnownValues(t *testing.T) {
	for _, test := range []struct {
		definition definition
		values     map[string]types.String
		wantPath   string
	}{
		{testDefinition(t, "ip_route"), map[string]types.String{"gateway": types.StringUnknown(), "distance": types.StringValue("")}, "distance"},
		{testDefinition(t, "nat_rule"), map[string]types.String{"chain": types.StringValue("srcnat"), "out-interface": types.StringUnknown(), "src-address": types.StringValue("0.0.0.0/0")}, "src_address"},
		{testDefinition(t, "firewall_rule"), map[string]types.String{"place-before": types.StringValue("")}, "place_before"},
	} {
		t.Run(test.wantPath, func(t *testing.T) {
			r := managedObject{definition: test.definition}
			plan := objectPlan(t, &r, test.values)
			var result resource.ValidateConfigResponse
			r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(plan)}, &result)
			if len(result.Diagnostics) != 1 {
				t.Fatalf("expected one diagnostic: %v", result.Diagnostics)
			}
			diagnostic, ok := result.Diagnostics[0].(diag.DiagnosticWithPath)
			if !ok || diagnostic.Path().String() != test.wantPath {
				t.Errorf("expected diagnostic at %s: %v", test.wantPath, result.Diagnostics)
			}
		})
	}
}

func TestProviderAliasesShareRuleLocksByEndpoint(t *testing.T) {
	r1 := managedObject{definition: testDefinition(t, "firewall_rule")}
	r2 := managedObject{definition: testDefinition(t, "firewall_rule")}
	for _, r := range []*managedObject{&r1, &r2} {
		r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: &routerClient{address: "router.example:8729"}}, &resource.ConfigureResponse{})
	}
	if r1.menuLock == nil || r1.menuLock != r2.menuLock || r1.menuLock == orderedMenuLock("other.example:8729", r1.menu) {
		t.Fatal("rule locks should be shared only by provider aliases to the same endpoint and menu")
	}
}

func TestNormalizationPreservesDrift(t *testing.T) {
	r := managedObject{definition: testDefinition(t, "firewall_rule")}
	for _, values := range [][3]string{
		{"src-address", "192.0.2.0/24", "192.0.3.0/24"},
		{"lease-time", "1h", "2h"},
		{"lease-time", "1 2h", "12h"},
	} {
		if r.normalize(values[0], values[1]) == r.normalize(values[0], values[2]) {
			t.Errorf("different values compared equal: %v", values)
		}
	}
}
