package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type runFunc func(context.Context, ...string) ([]map[string]string, string, error)

func (f runFunc) run(ctx context.Context, command ...string) ([]map[string]string, string, error) {
	return f(ctx, command...)
}

func TestWireGuardReadRequestsOnlyPublicFields(t *testing.T) {
	r := managedObject{
		definition: objects[0],
		client: runFunc(func(_ context.Context, command ...string) ([]map[string]string, string, error) {
			expected := []string{"/interface/wireguard/print", "=.proplist=.id,name,listen-port,mtu,comment,disabled,public-key", "?.id=*A"}
			if !slices.Equal(command, expected) {
				t.Fatalf("unexpected WireGuard read: %v", command)
			}
			return []map[string]string{{".id": "*A", "public-key": "public identity"}}, "", nil
		}),
	}
	row, err := r.read(t.Context(), "*a")
	if err != nil || row["public-key"] != "public identity" {
		t.Fatalf("read failed: %v", err)
	}
}

func TestReadDoesNotConfuseTransportErrorWithDeletion(t *testing.T) {
	denied := errors.New("permission denied")
	r := managedObject{definition: objects[0], client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
		return nil, "", denied
	})}
	if _, err := r.read(t.Context(), "*A"); !errors.Is(err, denied) {
		t.Fatalf("expected permission failure, got %v", err)
	}
}

func TestImportRejectsDynamicLeases(t *testing.T) {
	r := managedObject{
		definition: objects[7],
		client: runFunc(func(_ context.Context, args ...string) ([]map[string]string, string, error) {
			if !strings.Contains(args[1], ",dynamic") {
				t.Fatal("dynamic status omitted from projection")
			}
			return []map[string]string{{".id": "*A", "dynamic": "true"}}, "", nil
		}),
	}
	if _, err := r.read(t.Context(), "*A"); err == nil {
		t.Fatal("expected dynamic lease rejection")
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
		r := managedObject{definition: objects[9], client: client, menuLock: lock}
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
	r := managedObject{definition: objects[9], client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
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
	r := managedObject{definition: objects[9], client: runFunc(func(_ context.Context, words ...string) ([]map[string]string, string, error) {
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
		{"wireguard_peer", "persistent-keepalive", "25", "25s"},
		{"wireguard_peer", "allowed-address", "192.0.2.1, 2001:db8::1/128", "2001:db8::1,192.0.2.1/32"},
	} {
		r := managedObject{definition: definition{name: test.name}}
		if r.normalize(test.field, test.configured) != r.normalize(test.field, test.observed) {
			t.Errorf("%s %s: %q and %q did not compare equal", test.name, test.field, test.configured, test.observed)
		}
	}
}
