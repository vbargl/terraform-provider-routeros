package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// fakeLists is a test double for /container/envs and /container/mounts: one row
// per entry, grouped by list name, as RouterOS stores them.
type fakeLists struct {
	t        *testing.T
	rows     map[string][]map[string]string
	nextID   int
	commands [][]string
}

func newFakeLists(t *testing.T) *fakeLists {
	return &fakeLists{t: t, rows: map[string][]map[string]string{}, nextID: 0x10}
}

func (f *fakeLists) seed(menu string, row map[string]string) {
	row[".id"] = fmt.Sprintf("*%X", f.nextID)
	f.nextID++
	if row["disabled"] == "" {
		row["disabled"] = "false"
	}
	f.rows[menu] = append(f.rows[menu], row)
}

func (f *fakeLists) run(_ context.Context, command ...string) ([]map[string]string, string, error) {
	f.commands = append(f.commands, command)
	slash := strings.LastIndex(command[0], "/")
	menu, verb := command[0][:slash], command[0][slash+1:]
	args := words(command)
	switch verb {
	case "print":
		var result []map[string]string
		for _, row := range f.rows[menu] {
			if want, ok := strings.CutPrefix(command[2], "?list="); ok && row["list"] != want {
				continue
			}
			copied := map[string]string{}
			for key, value := range row {
				copied[key] = value
			}
			result = append(result, copied)
		}
		return result, "", nil
	case "add":
		row := map[string]string{}
		for key, value := range args {
			row[key] = value
		}
		f.seed(menu, row)
		return nil, row[".id"], nil
	case "set":
		for _, row := range f.rows[menu] {
			if row[".id"] == args[".id"] {
				for key, value := range args {
					row[key] = value
				}
				if args["disabled"] == "no" {
					row["disabled"] = "false"
				}
			}
		}
		return nil, "", nil
	case "remove":
		f.rows[menu] = slices.DeleteFunc(f.rows[menu], func(row map[string]string) bool { return row[".id"] == args[".id"] })
		return nil, "", nil
	}
	return nil, "", fmt.Errorf("unexpected command %v", command)
}

func (f *fakeLists) list(menu, name, key string) map[string]string {
	result := map[string]string{}
	for _, row := range f.rows[menu] {
		if row["list"] == name {
			result[row[key]] = row[".id"]
		}
	}
	return result
}

func (f *fakeLists) writes() []string {
	var result []string
	for _, command := range f.commands {
		if !strings.HasSuffix(command[0], "/print") {
			result = append(result, strings.Join(command, " "))
		}
	}
	return result
}

// listPlan builds a plan for an env list (values: key => value) or a mount list
// (values: dst => "src mode").
func listPlan(t *testing.T, r *containerList, name string, values map[string]string) tfsdk.Plan {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &resp)
	objectType := resp.Schema.Type().TerraformType(t.Context()).(tftypes.Object)
	mapType := objectType.AttributeTypes[r.attribute].(tftypes.Map)
	elements := map[string]tftypes.Value{}
	for key, value := range values {
		if r.attribute == "env" {
			elements[key] = tftypes.NewValue(tftypes.String, value)
			continue
		}
		src, mode, _ := strings.Cut(value, " ")
		elements[key] = tftypes.NewValue(mapType.ElementType, map[string]tftypes.Value{
			"src": tftypes.NewValue(tftypes.String, src), "mode": tftypes.NewValue(tftypes.String, mode),
		})
	}
	raw := map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":      tftypes.NewValue(tftypes.String, name),
		r.attribute: tftypes.NewValue(mapType, elements),
	}
	return tfsdk.Plan{Schema: resp.Schema, Raw: tftypes.NewValue(objectType, raw)}
}

func stateEnv(t *testing.T, state tfsdk.State) map[string]string {
	t.Helper()
	var values types.Map
	state.GetAttribute(t.Context(), attr("env"), &values)
	result := map[string]string{}
	values.ElementsAs(t.Context(), &result, false)
	return result
}

func TestEnvListConvergesEntriesIndividually(t *testing.T) {
	fake := newFakeLists(t)
	fake.seed("/container/envs", map[string]string{"list": "other", "key": "POOL", "value": "keep"})
	r := &containerList{listSpec: envListSpec, client: fake}
	plan := listPlan(t, r, "agent", map[string]string{"KNOCKERUP_NAMESPACE": "knockerup", "POOL": "suspendable", "HOME": "/"})
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	if got := fake.list("/container/envs", "agent", "key"); len(got) != 3 {
		t.Fatalf("entries = %v", got)
	}
	if env := stateEnv(t, created.State); env["POOL"] != "suspendable" || len(env) != 3 || stateValue(t, created.State, "id") != "agent" {
		t.Fatalf("state = %v", env)
	}
	before := fake.list("/container/envs", "agent", "key")

	// Remove one entry, add one and change one: only those entries are written.
	fake.commands = nil
	next := listPlan(t, r, "agent", map[string]string{"KNOCKERUP_NAMESPACE": "knockerup", "POOL": "other-pool", "TZ": "UTC"})
	updated := resource.UpdateResponse{State: created.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: next, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	writes := fake.writes()
	expected := []string{
		"/container/envs/remove =.id=" + before["HOME"],
		"/container/envs/set =.id=" + before["POOL"] + " =value=other-pool",
		"/container/envs/add =list=agent =key=TZ =value=UTC",
	}
	slices.Sort(writes)
	slices.Sort(expected)
	if !slices.Equal(writes, expected) {
		t.Fatalf("writes = %v, want %v", writes, expected)
	}
	after := fake.list("/container/envs", "agent", "key")
	if after["KNOCKERUP_NAMESPACE"] != before["KNOCKERUP_NAMESPACE"] || after["POOL"] != before["POOL"] {
		t.Fatal("unchanged entries were recreated")
	}
	if fake.list("/container/envs", "other", "key")["POOL"] == "" {
		t.Fatal("another list was modified")
	}

	deleted := resource.DeleteResponse{}
	r.Delete(t.Context(), resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() || len(fake.list("/container/envs", "agent", "key")) != 0 || len(fake.list("/container/envs", "other", "key")) != 1 {
		t.Fatalf("delete must remove exactly this list: %v", fake.rows)
	}
}

func TestEnvListReportsAndRemovesUnmanagedEntries(t *testing.T) {
	fake := newFakeLists(t)
	r := &containerList{listSpec: envListSpec, client: fake}
	plan := listPlan(t, r, "agent", map[string]string{"A": "1", "B": "2"})
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)

	// Someone adds an entry, duplicates a key and disables another on the router.
	fake.seed("/container/envs", map[string]string{"list": "agent", "key": "EXTRA", "value": "x"})
	fake.seed("/container/envs", map[string]string{"list": "agent", "key": "A", "value": "1"})
	for _, row := range fake.rows["/container/envs"] {
		if row["key"] == "B" {
			row["disabled"] = "true"
		}
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	env := stateEnv(t, read.State)
	if _, ok := env["EXTRA"]; !ok || env["A"] != "" || env["B"] != "" || len(env) != 1 {
		t.Fatalf("drift not visible: %v", env)
	}

	updated := resource.UpdateResponse{State: read.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: plan, State: read.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	keys := fake.list("/container/envs", "agent", "key")
	if len(fake.rows["/container/envs"]) != 2 || keys["A"] == "" || keys["B"] == "" {
		t.Fatalf("list did not converge: %v", fake.rows["/container/envs"])
	}
	if env := stateEnv(t, updated.State); len(env) != 2 || env["B"] != "2" {
		t.Fatalf("state after converge = %v", env)
	}
}

func TestContainerListCreateRefusesExistingListAndImports(t *testing.T) {
	fake := newFakeLists(t)
	fake.seed("/container/mounts", map[string]string{"list": "agent", "dst": "/etc/agent", "src": "/usb1/agent", "mode": "ro"})
	r := &containerList{listSpec: mountListSpec, client: fake}
	plan := listPlan(t, r, "agent", map[string]string{"/etc/agent": "/usb1/agent ro"})
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() || len(fake.writes()) != 0 {
		t.Fatal("creating over an existing list must fail without writing")
	}

	imported := resource.ImportStateResponse{State: emptyState(plan)}
	r.ImportState(t.Context(), resource.ImportStateRequest{ID: "agent"}, &imported)
	read := resource.ReadResponse{State: imported.State}
	r.Read(t.Context(), resource.ReadRequest{State: imported.State}, &read)
	if read.Diagnostics.HasError() || stateValue(t, read.State, "name") != "agent" {
		t.Fatal(read.Diagnostics)
	}
	var mounts map[string]struct {
		Src  string `tfsdk:"src"`
		Mode string `tfsdk:"mode"`
	}
	var value types.Map
	read.State.GetAttribute(t.Context(), attr("mount"), &value)
	value.ElementsAs(t.Context(), &mounts, false)
	if mounts["/etc/agent"].Src != "/usb1/agent" || mounts["/etc/agent"].Mode != "ro" {
		t.Fatalf("imported mounts = %v", mounts)
	}

	// Change a mode and add a destination.
	next := listPlan(t, r, "agent", map[string]string{"/etc/agent": "/usb1/agent ro,noexec", "/etc/agent-tls": "/usb1/ca ro"})
	updated := resource.UpdateResponse{State: read.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: next, State: read.State}, &updated)
	writes := fake.writes()
	if updated.Diagnostics.HasError() || len(writes) != 2 || !strings.Contains(writes[0], "=mode=ro,noexec") || !strings.Contains(writes[1], "=dst=/etc/agent-tls") {
		t.Fatalf("writes = %v, %v", writes, updated.Diagnostics)
	}
}

func TestContainerListValidation(t *testing.T) {
	env := &containerList{listSpec: envListSpec}
	mount := &containerList{listSpec: mountListSpec}
	for label, tc := range map[string]struct {
		r      *containerList
		name   string
		values map[string]string
	}{
		"empty list":    {env, "agent", map[string]string{}},
		"comma in name": {env, "a,b", map[string]string{"A": "1"}},
		"bad env key":   {env, "agent", map[string]string{"1BAD": "1"}},
		"relative dst":  {mount, "agent", map[string]string{"etc": "/usb1 ro"}},
		"relative src":  {mount, "agent", map[string]string{"/etc": "usb1 ro"}},
		"bad mode":      {mount, "agent", map[string]string{"/etc": "/usb1 readonly"}},
	} {
		resp := resource.ValidateConfigResponse{}
		tc.r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(listPlan(t, tc.r, tc.name, tc.values))}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatalf("%s: expected a validation error", label)
		}
	}
	resp := resource.ValidateConfigResponse{}
	mount.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(listPlan(t, mount, "agent", map[string]string{"/etc/knockerup/kubeconfig": "/knockerup/kubeconfig ro"}))}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
}

func TestRenamingAContainerListReplacesIt(t *testing.T) {
	for _, spec := range []listSpec{envListSpec, mountListSpec} {
		var resp resource.SchemaResponse
		(&containerList{listSpec: spec}).Schema(t.Context(), resource.SchemaRequest{}, &resp)
		replaces := false
		for _, modifier := range resp.Schema.Attributes["name"].(schema.StringAttribute).PlanModifiers {
			replaces = replaces || strings.Contains(modifier.Description(t.Context()), "destroy and recreate")
		}
		if !replaces {
			t.Fatalf("%s: renaming a list must replace it", spec.resource)
		}
	}
}

func TestContainerReferencesSeveralLists(t *testing.T) {
	fake := newFakeContainers(t)
	values := map[string]string{}
	for key, value := range knockerupperPlan {
		values[key] = value
	}
	values["envlists"], values["mountlists"] = "common,knockerupper", "common,knockerupper,secrets"
	r := &container{client: fake}
	plan := resourcePlan(t, r, values)
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	add := fake.sent("/container/add")[0]
	if !slices.Contains(add, "=envlists=common,knockerupper") || !slices.Contains(add, "=mountlists=common,knockerupper,secrets") {
		t.Fatalf("lists must be sent comma-separated in order: %v", add)
	}
	fake.rows["*1"]["envlists"] = "knockerupper,common"
	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	if !read.State.Raw.Equal(created.State.Raw) {
		t.Fatal("a reordered list reported as drift")
	}
	fake.rows["*1"]["envlists"] = "knockerupper"
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	var names []string
	var envlists types.List
	read.State.GetAttribute(t.Context(), attr("envlists"), &envlists)
	envlists.ElementsAs(t.Context(), &names, false)
	if !slices.Equal(names, []string{"knockerupper"}) {
		t.Fatalf("removed list not observed: %v", names)
	}
	for _, lists := range []string{"a,a", "a b"} {
		values["envlists"] = lists
		resp := resource.ValidateConfigResponse{}
		r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(resourcePlan(t, r, values))}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatalf("%q: expected a validation error", lists)
		}
	}
}
