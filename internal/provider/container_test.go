package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// fakeContainers is a RouterOS /container test double. Each read advances a
// transitional state by one step.
type fakeContainers struct {
	t        *testing.T
	rows     map[string]map[string]string
	steps    map[string][]string
	commands [][]string
	failPull bool
}

func newFakeContainers(t *testing.T) *fakeContainers {
	t.Helper()
	previous := containerPollInterval
	containerPollInterval = time.Millisecond
	t.Cleanup(func() { containerPollInterval = previous })
	return &fakeContainers{t: t, rows: map[string]map[string]string{}, steps: map[string][]string{}}
}

func (f *fakeContainers) setState(id, state string) {
	row := f.rows[id]
	for _, flag := range containerFlags {
		delete(row, flag)
	}
	if state != "" {
		row[state] = "true"
	}
}

func words(command []string) map[string]string {
	values := map[string]string{}
	for _, word := range command[1:] {
		if key, value, ok := strings.Cut(strings.TrimPrefix(word, "="), "="); ok {
			values[key] = value
		}
	}
	return values
}

func (f *fakeContainers) run(_ context.Context, command ...string) ([]map[string]string, string, error) {
	f.commands = append(f.commands, command)
	args := words(command)
	switch command[0] {
	case "/container/add":
		id := "*1"
		row := map[string]string{".id": id, "logging": "false", "check-certificate": "true", "start-on-boot": "false", "restart-policy": "no"}
		for key, value := range args {
			row[key] = value
		}
		f.rows[id] = row
		f.setState(id, "downloading/extracting")
		f.steps[id] = []string{"downloading/extracting", "stopped"}
		if f.failPull {
			f.steps[id] = []string{"downloading/extracting", ""}
		}
		return nil, id, nil
	case "/container/print":
		if !strings.Contains(command[1], "downloading/extracting") || !strings.Contains(command[1], "remote-image") {
			f.t.Fatalf("incomplete container projection: %v", command)
		}
		id := strings.TrimPrefix(command[2], "?.id=")
		row, ok := f.rows[id]
		if !ok {
			return nil, "", nil
		}
		snapshot := map[string]string{}
		for key, value := range row {
			snapshot[key] = value
		}
		if steps := f.steps[id]; len(steps) > 0 {
			f.steps[id] = steps[1:]
			if steps[0] == "gone" {
				delete(f.rows, id)
			} else {
				f.setState(id, steps[0])
			}
		}
		return []map[string]string{snapshot}, "", nil
	case "/container/start":
		f.steps[args[".id"]] = []string{"starting", "running"}
	case "/container/stop":
		f.steps[args[".id"]] = []string{"stopping", "stopped"}
	case "/container/remove":
		f.steps[args[".id"]] = []string{"deleting", "gone"}
	case "/container/set":
		for key, value := range args {
			f.rows[args[".id"]][key] = value
		}
	default:
		return nil, "", errors.New("unexpected command " + command[0])
	}
	return nil, "", nil
}

func (f *fakeContainers) sent(name string) [][]string {
	var result [][]string
	for _, command := range f.commands {
		if command[0] == name {
			result = append(result, command)
		}
	}
	return result
}

func resourcePlan(t *testing.T, r resource.Resource, values map[string]string) tfsdk.Plan {
	t.Helper()
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	objectType := schema.Schema.Type().TerraformType(t.Context()).(tftypes.Object)
	raw := map[string]tftypes.Value{}
	for name, attributeType := range objectType.AttributeTypes {
		raw[name] = tftypes.NewValue(attributeType, nil)
		if name == "id" || name == "state" {
			raw[name] = tftypes.NewValue(attributeType, tftypes.UnknownValue)
		}
	}
	for name, value := range values {
		raw[name] = tftypes.NewValue(tftypes.String, value)
	}
	return tfsdk.Plan{Schema: schema.Schema, Raw: tftypes.NewValue(objectType, raw)}
}

func stateValue(t *testing.T, state tfsdk.State, name string) string {
	t.Helper()
	values := map[string]tftypes.Value{}
	if err := state.Raw.As(&values); err != nil {
		t.Fatal(err)
	}
	if values[name].IsNull() {
		return "<null>"
	}
	var result string
	if err := values[name].As(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

var knockerupperPlan = map[string]string{
	"name": "knockerupper", "remote_image": "vbargl/knockerupper:test", "interface": "veth-knock", "root_dir": "tmpfs1/knockerupper",
	"envlists": "knockerupper", "mountlists": "knockerupper", "logging": "yes", "restart_policy": "always",
	"check_certificate": "yes", "start_on_boot": "yes", "running": "yes", "comment": "test",
}

func TestContainerLifecycleWaitsForExtractionAndRunState(t *testing.T) {
	fake := newFakeContainers(t)
	r := &container{client: fake}
	plan := resourcePlan(t, r, knockerupperPlan)
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	add := fake.sent("/container/add")[0]
	for _, word := range []string{"=remote-image=vbargl/knockerupper:test", "=root-dir=tmpfs1/knockerupper", "=start-on-boot=yes"} {
		if !slices.Contains(add, word) {
			t.Fatalf("add is missing %s: %v", word, add)
		}
	}
	if slices.ContainsFunc(add, func(word string) bool {
		return strings.HasPrefix(word, "=running=") || strings.HasPrefix(word, "=file=")
	}) {
		t.Fatalf("add sent a pseudo or unset field: %v", add)
	}
	if len(fake.sent("/container/start")) != 1 {
		t.Fatal("container was not started after extraction")
	}
	if got := stateValue(t, created.State, "state"); got != "running" {
		t.Fatalf("state = %s", got)
	}
	if got := stateValue(t, created.State, "logging"); got != "yes" {
		t.Fatalf("logging = %s, want normalized yes", got)
	}
	if got := stateValue(t, created.State, "file"); got != "" {
		t.Fatalf("unset file = %q, want empty", got)
	}

	// A crashed container reads as not running; the default running=yes then starts it in place.
	fake.setState("*1", "stopped")
	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	if got := stateValue(t, read.State, "running"); got != "no" {
		t.Fatalf("running after crash = %s", got)
	}
	updated := resource.UpdateResponse{State: read.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: tfsdk.Plan(created.State), State: read.State}, &updated)
	if updated.Diagnostics.HasError() || len(fake.sent("/container/start")) != 2 || stateValue(t, updated.State, "running") != "yes" {
		t.Fatalf("restart failed: %v", updated.Diagnostics)
	}
	if len(fake.sent("/container/set")) != 0 {
		t.Fatal("unchanged in-place fields were written")
	}

	deleted := resource.DeleteResponse{}
	r.Delete(t.Context(), resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal(deleted.Diagnostics)
	}
	if len(fake.sent("/container/stop")) != 1 || len(fake.sent("/container/remove")) != 1 || len(fake.rows) != 0 {
		t.Fatalf("container was not stopped before removal: %v", fake.commands)
	}
}

func TestContainerCreateKeepsBindingWhenExtractionFails(t *testing.T) {
	fake := newFakeContainers(t)
	fake.failPull = true
	previous := containerExtractTimeout
	containerExtractTimeout = 20 * time.Millisecond
	t.Cleanup(func() { containerExtractTimeout = previous })
	r := &container{client: fake}
	plan := resourcePlan(t, r, knockerupperPlan)
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() {
		t.Fatal("expected an extraction error")
	}
	if got := stateValue(t, created.State, "id"); got != "*1" {
		t.Fatalf("binding lost after failed extraction: %s", got)
	}
	if len(fake.sent("/container/start")) != 0 {
		t.Fatal("started a container whose image is not ready")
	}
}

func TestContainerUpdatesInPlaceFieldsOnly(t *testing.T) {
	fake := newFakeContainers(t)
	r := &container{client: fake}
	plan := resourcePlan(t, r, knockerupperPlan)
	created := resource.CreateResponse{State: emptyState(plan)}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
	changed := map[string]string{}
	for key, value := range knockerupperPlan {
		changed[key] = value
	}
	changed["start_on_boot"], changed["comment"], changed["running"] = "no", "changed", "no"
	next := resourcePlan(t, r, changed)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: next, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	set := fake.sent("/container/set")
	if len(set) != 1 || !slices.Contains(set[0], "=start-on-boot=no") || !slices.Contains(set[0], "=comment=changed") || len(set[0]) != 4 {
		t.Fatalf("unexpected set: %v", set)
	}
	if len(fake.sent("/container/stop")) != 1 || stateValue(t, updated.State, "state") != "stopped" {
		t.Fatal("running=no did not stop the container")
	}
}

func TestContainerSchemaReplacesOnImageChange(t *testing.T) {
	var resp resource.SchemaResponse
	(&container{}).Schema(t.Context(), resource.SchemaRequest{}, &resp)
	replaces := func(name string) bool {
		attribute := resp.Schema.Attributes[name].(schema.StringAttribute)
		for _, modifier := range attribute.PlanModifiers {
			if strings.Contains(modifier.Description(t.Context()), "destroy and recreate") {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"remote_image", "file", "root_dir", "envlists", "mountlists", "interface", "name", "restart_policy"} {
		if !replaces(name) {
			t.Fatalf("%s must require replacement", name)
		}
	}
	for _, name := range []string{"start_on_boot", "comment", "running"} {
		if replaces(name) {
			t.Fatalf("%s must change in place", name)
		}
	}
}

func TestContainerValidation(t *testing.T) {
	r := &container{}
	for label, values := range map[string]map[string]string{
		"no image":       {"name": "a", "interface": "veth", "root_dir": "disk1/a"},
		"two images":     {"name": "a", "interface": "veth", "root_dir": "disk1/a", "remote_image": "x", "file": "x.tar"},
		"bad policy":     {"name": "a", "interface": "veth", "root_dir": "disk1/a", "remote_image": "x", "restart_policy": "sometimes"},
		"bad running":    {"name": "a", "interface": "veth", "root_dir": "disk1/a", "remote_image": "x", "running": "true"},
		"empty root dir": {"name": "a", "interface": "veth", "root_dir": "", "remote_image": "x"},
	} {
		plan := resourcePlan(t, r, values)
		resp := resource.ValidateConfigResponse{}
		r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(plan)}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatalf("%s: expected a validation error", label)
		}
	}
	plan := resourcePlan(t, r, knockerupperPlan)
	resp := resource.ValidateConfigResponse{}
	r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
}
