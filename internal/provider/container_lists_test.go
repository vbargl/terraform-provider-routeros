package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// fakeRouter combines the /container test double with list menus that store
// rows the way RouterOS does: one row per env or mount entry, grouped by list.
type fakeRouter struct {
	*fakeContainers
	menus  map[string][]map[string]string
	nextID int
}

func newFakeRouter(t *testing.T) *fakeRouter {
	return &fakeRouter{fakeContainers: newFakeContainers(t), menus: map[string][]map[string]string{}, nextID: 0x10}
}

func (f *fakeRouter) run(ctx context.Context, command ...string) ([]map[string]string, string, error) {
	slash := strings.LastIndex(command[0], "/")
	menu, verb := command[0][:slash], command[0][slash+1:]
	if menu != "/container/envs" && menu != "/container/mounts" {
		return f.fakeContainers.run(ctx, command...)
	}
	f.commands = append(f.commands, command)
	args := words(command)
	switch verb {
	case "add":
		id := fmt.Sprintf("*%X", f.nextID)
		f.nextID++
		row := map[string]string{".id": id, "disabled": "false"}
		for key, value := range args {
			row[key] = value
		}
		f.menus[menu] = append(f.menus[menu], row)
		return nil, id, nil
	case "print":
		var result []map[string]string
		for _, row := range f.menus[menu] {
			match := true
			for _, word := range command[1:] {
				if key, value, ok := strings.Cut(strings.TrimPrefix(word, "?"), "="); ok && strings.HasPrefix(word, "?") && !strings.EqualFold(row[key], value) {
					match = false
				}
			}
			if match {
				copied := map[string]string{}
				for key, value := range row {
					copied[key] = value
				}
				result = append(result, copied)
			}
		}
		return result, "", nil
	case "set":
		for _, row := range f.menus[menu] {
			if row[".id"] == args[".id"] {
				for key, value := range args {
					row[key] = value
				}
			}
		}
		return nil, "", nil
	case "remove":
		f.menus[menu] = slices.DeleteFunc(f.menus[menu], func(row map[string]string) bool { return row[".id"] == args[".id"] })
		return nil, "", nil
	}
	return nil, "", fmt.Errorf("unexpected command %v", command)
}

func (f *fakeRouter) entries(menu, list, field string) []string {
	var result []string
	for _, row := range f.menus[menu] {
		if row["list"] == list {
			result = append(result, row[field])
		}
	}
	return result
}

// containerCommands counts commands that would change a container.
func (f *fakeRouter) containerCommands() int {
	count := 0
	for _, command := range f.commands {
		if strings.HasPrefix(command[0], "/container/") && !strings.HasPrefix(command[0], "/container/envs") &&
			!strings.HasPrefix(command[0], "/container/mounts") && command[0] != "/container/print" {
			count++
		}
	}
	return count
}

func createObject(t *testing.T, client commandRunner, name string, values map[string]string) (*managedObject, resource.CreateResponse) {
	t.Helper()
	r := &managedObject{definition: testDefinition(t, name), client: client}
	if _, ok := values["disabled"]; !ok {
		values["disabled"] = "no"
	}
	if _, ok := values["comment"]; !ok {
		values["comment"] = ""
	}
	plan := resourcePlan(t, r, values)
	resp := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &resp)
	return r, resp
}

func TestOneContainerUsesListsWithSeveralEntries(t *testing.T) {
	fake := newFakeRouter(t)
	envs := map[string]resource.CreateResponse{}
	for _, entry := range [][2]string{{"POD_NAMESPACE", "knockerup-system"}, {"POOL", "suspendable"}, {"IDENTITY", "stargate"}} {
		_, resp := createObject(t, fake, "container_env", map[string]string{"list": "knockerupper", "key": entry[0], "value": entry[1]})
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		envs[entry[0]] = resp
	}
	for _, dst := range []string{"/etc/knockerup", "/var/run/knockerup"} {
		_, resp := createObject(t, fake, "container_mount", map[string]string{"list": "knockerupper", "src": "/usb1" + dst, "dst": dst, "mode": "ro"})
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
	}
	values := map[string]string{}
	for key, value := range knockerupperPlan {
		values[key] = value
	}
	r := &container{client: fake}
	plan := resourcePlan(t, r, values)
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	add := fake.sent("/container/add")[0]
	if !slices.Contains(add, "=envlists=knockerupper") || !slices.Contains(add, "=mountlists=knockerupper") {
		t.Fatalf("container must reference the lists by name: %v", add)
	}
	if got := fake.entries("/container/envs", "knockerupper", "key"); !slices.Equal(got, []string{"POD_NAMESPACE", "POOL", "IDENTITY"}) {
		t.Fatalf("env entries = %v", got)
	}
	if got := fake.entries("/container/mounts", "knockerupper", "dst"); len(got) != 2 {
		t.Fatalf("mount entries = %v", got)
	}

	// Every entry reads back unchanged and independently.
	for key, resp := range envs {
		r := &managedObject{definition: testDefinition(t, "container_env"), client: fake}
		read := resource.ReadResponse{State: resp.State}
		r.Read(t.Context(), resource.ReadRequest{State: resp.State}, &read)
		if read.Diagnostics.HasError() || stateValue(t, read.State, "key") != key || read.State.Raw.IsNull() {
			t.Fatalf("%s did not read back: %v", key, read.Diagnostics)
		}
	}

	// Removing one entry and adding another leaves the container and the other entries alone.
	before := fake.containerCommands()
	envResource := &managedObject{definition: testDefinition(t, "container_env"), client: fake}
	deleted := resource.DeleteResponse{}
	envResource.Delete(t.Context(), resource.DeleteRequest{State: envs["POOL"].State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal(deleted.Diagnostics)
	}
	if _, resp := createObject(t, fake, "container_env", map[string]string{"list": "knockerupper", "key": "OTLP_ENDPOINT", "value": "http://metrics:9090"}); resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if got := fake.entries("/container/envs", "knockerupper", "key"); !slices.Equal(got, []string{"POD_NAMESPACE", "IDENTITY", "OTLP_ENDPOINT"}) {
		t.Fatalf("env entries after change = %v", got)
	}
	if fake.containerCommands() != before {
		t.Fatal("changing list entries touched the container")
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	if !read.State.Raw.Equal(created.State.Raw) {
		t.Fatal("container state drifted after list entries changed")
	}
}

func TestContainerReferencesSeveralLists(t *testing.T) {
	fake := newFakeRouter(t)
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
	var names []string
	var envlists types.List
	created.State.GetAttribute(t.Context(), attr("envlists"), &envlists)
	envlists.ElementsAs(t.Context(), &names, false)
	if !slices.Equal(names, []string{"common", "knockerupper"}) {
		t.Fatalf("envlists = %v", names)
	}

	// RouterOS returning the same names in another order is not drift.
	fake.rows["*1"]["envlists"] = "knockerupper,common"
	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	if !read.State.Raw.Equal(created.State.Raw) {
		t.Fatal("a reordered list reported as drift")
	}
	// A different set of names is drift.
	fake.rows["*1"]["envlists"] = "knockerupper"
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	read.State.GetAttribute(t.Context(), attr("envlists"), &envlists)
	envlists.ElementsAs(t.Context(), &names, false)
	if !slices.Equal(names, []string{"knockerupper"}) {
		t.Fatalf("removed list not observed: %v", names)
	}
}

func TestContainerListReferencesAreValidated(t *testing.T) {
	r := &container{}
	for label, lists := range map[string]string{"duplicate": "a,a", "space": "a b"} {
		values := map[string]string{}
		for key, value := range knockerupperPlan {
			values[key] = value
		}
		values["envlists"] = lists
		plan := resourcePlan(t, r, values)
		resp := resource.ValidateConfigResponse{}
		r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(plan)}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatalf("%s: expected a validation error", label)
		}
	}
	for _, name := range []string{"container_env", "container_mount"} {
		if !validateList(t, managedObject{definition: testDefinition(t, name)}, "a,b") {
			t.Fatalf("%s: a list name with a comma must be rejected", name)
		}
	}
}

func validateList(t *testing.T, r managedObject, list string) bool {
	t.Helper()
	plan := resourcePlan(t, &r, map[string]string{"list": list})
	resp := resource.ValidateConfigResponse{}
	r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(plan)}, &resp)
	return resp.Diagnostics.HasError()
}

func TestDuplicateEntriesInOneListAreRejected(t *testing.T) {
	fake := newFakeRouter(t)
	for _, tc := range []struct {
		name  string
		first map[string]string
		dup   map[string]string
		other map[string]string
	}{
		{
			"container_env",
			map[string]string{"list": "agent", "key": "POOL", "value": "a"},
			map[string]string{"list": "agent", "key": "POOL", "value": "b"},
			map[string]string{"list": "other", "key": "POOL", "value": "b"},
		},
		{
			"container_mount",
			map[string]string{"list": "agent", "src": "/usb1/a", "dst": "/etc/agent", "mode": "ro"},
			map[string]string{"list": "agent", "src": "/usb1/b", "dst": "/etc/agent", "mode": "ro"},
			map[string]string{"list": "other", "src": "/usb1/b", "dst": "/etc/agent", "mode": "ro"},
		},
	} {
		r, first := createObject(t, fake, tc.name, tc.first)
		if first.Diagnostics.HasError() {
			t.Fatal(first.Diagnostics)
		}
		adds := len(fake.commands)
		_, dup := createObject(t, fake, tc.name, tc.dup)
		if !dup.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(dup.Diagnostics), "Duplicate container list entry") {
			t.Fatalf("%s: duplicate in one list accepted: %v", tc.name, dup.Diagnostics)
		}
		for _, command := range fake.commands[adds:] {
			if strings.HasSuffix(command[0], "/add") {
				t.Fatalf("%s: duplicate reached the router", tc.name)
			}
		}
		if _, other := createObject(t, fake, tc.name, tc.other); other.Diagnostics.HasError() {
			t.Fatalf("%s: the same key in another list must be allowed: %v", tc.name, other.Diagnostics)
		}

		// Updating an entry in place is not a duplicate of itself.
		values := map[string]string{"disabled": "no", "comment": "updated"}
		for key, value := range tc.first {
			values[key] = value
		}
		plan := resourcePlan(t, r, values)
		updated := resource.UpdateResponse{State: first.State}
		r.Update(t.Context(), resource.UpdateRequest{Plan: plan, State: first.State}, &updated)
		if updated.Diagnostics.HasError() {
			t.Fatalf("%s: in-place update rejected: %v", tc.name, updated.Diagnostics)
		}
	}
}
