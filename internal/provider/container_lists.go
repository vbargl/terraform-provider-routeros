package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	tfattr "github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// A container list resource exclusively owns every RouterOS entry that carries
// its list name: env variables in /container/envs or mounts in /container/mounts.
// Entries are keyed by the env key or the mount destination, so a map in the
// configuration cannot hold duplicates. Entries are converged one by one.
type listSpec struct {
	resource  string   // Terraform type suffix
	menu      string   // RouterOS menu
	attribute string   // Terraform map attribute
	key       string   // RouterOS field used as map key
	fields    []string // RouterOS fields of one entry besides the key
}

var (
	envListSpec   = listSpec{resource: "container_env_list", menu: "/container/envs", attribute: "env", key: "key", fields: []string{"value"}}
	mountListSpec = listSpec{resource: "container_mount_list", menu: "/container/mounts", attribute: "mount", key: "dst", fields: []string{"src", "mode"}}
)

var mountEntryType = types.ObjectType{AttrTypes: map[string]tfattr.Type{"src": types.StringType, "mode": types.StringType}}

var mountModes = []string{"ro", "ro,noexec", "rw", "rw,noexec"}

type containerList struct {
	listSpec
	client   commandRunner
	menuLock *sync.Mutex
}

// entries maps the key (env key or mount destination) to the entry's other fields.
type entries map[string]map[string]string

func (r *containerList) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.resource
}

func (r *containerList) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Description: "List name referenced by routeros_container. This resource owns every RouterOS entry with this name."},
	}
	description := "Manage a RouterOS container environment list. Every /container/envs entry with this list name is managed: entries missing from env are removed."
	if r.attribute == "env" {
		attributes["env"] = schema.MapAttribute{ElementType: types.StringType, Required: true,
			Description: "Environment variables by name. Values are stored in state and readable on the router; use non-secret values only."}
	} else {
		description = "Manage a RouterOS container mount list. Every /container/mounts entry with this list name is managed: entries missing from mount are removed."
		attributes["mount"] = schema.MapNestedAttribute{Required: true,
			Description: "Mounts by absolute destination path in the container.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"src":  schema.StringAttribute{Required: true, Description: "Absolute source path on the router."},
				"mode": schema.StringAttribute{Required: true, Description: "ro, ro,noexec, rw or rw,noexec."},
			}},
		}
	}
	resp.Schema = schema.Schema{Description: description, Attributes: attributes}
}

func (r *containerList) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
	if client, ok := req.ProviderData.(*routerClient); ok {
		r.menuLock = orderedMenuLock(client.address, r.menu)
	}
}

// desired reads the configured map from a plan, state or config.
func (r *containerList) desired(ctx context.Context, source objectAttributes, diagnostics *diag.Diagnostics) (entries, bool) {
	result := entries{}
	if r.attribute == "env" {
		var values types.Map
		diagnostics.Append(source.GetAttribute(ctx, path.Root("env"), &values)...)
		if values.IsNull() || values.IsUnknown() {
			return nil, false
		}
		for key, value := range values.Elements() {
			text, ok := value.(types.String)
			if !ok || text.IsUnknown() {
				return nil, false
			}
			result[key] = map[string]string{"value": text.ValueString()}
		}
		return result, true
	}
	var values types.Map
	diagnostics.Append(source.GetAttribute(ctx, path.Root("mount"), &values)...)
	if values.IsNull() || values.IsUnknown() {
		return nil, false
	}
	for dst, value := range values.Elements() {
		object, ok := value.(types.Object)
		if !ok || object.IsUnknown() {
			return nil, false
		}
		entry := map[string]string{}
		for _, field := range r.fields {
			text, ok := object.Attributes()[field].(types.String)
			if !ok || text.IsUnknown() {
				return nil, false
			}
			entry[field] = text.ValueString()
		}
		result[dst] = entry
	}
	return result, true
}

func (r *containerList) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	if !name.IsNull() && !name.IsUnknown() && !listName.MatchString(name.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid list name", "Use a non-empty name without commas or spaces; containers reference several lists as a comma-separated value.")
	}
	values, known := r.desired(ctx, req.Config, &resp.Diagnostics)
	if !known {
		return
	}
	at := path.Root(r.attribute)
	if len(values) == 0 {
		resp.Diagnostics.AddAttributeError(at, "Empty container list", "A RouterOS list exists only through its entries. Configure at least one entry.")
	}
	for key, entry := range values {
		if r.attribute == "env" {
			if !envKey.MatchString(key) {
				resp.Diagnostics.AddAttributeError(at.AtMapKey(key), "Invalid environment variable name", "Use letters, digits and underscores, not starting with a digit.")
			}
			continue
		}
		if !strings.HasPrefix(key, "/") {
			resp.Diagnostics.AddAttributeError(at.AtMapKey(key), "Invalid mount destination", "Use an absolute path in the container.")
		}
		if !strings.HasPrefix(entry["src"], "/") {
			resp.Diagnostics.AddAttributeError(at.AtMapKey(key).AtName("src"), "Invalid mount source", "Use an absolute path on the router.")
		}
		if !slices.Contains(mountModes, entry["mode"]) {
			resp.Diagnostics.AddAttributeError(at.AtMapKey(key).AtName("mode"), "Invalid mount mode", "Use ro, ro,noexec, rw or rw,noexec.")
		}
	}
}

func (r *containerList) rows(ctx context.Context, name string) ([]map[string]string, error) {
	fields := append([]string{".id", "list", r.key}, r.fields...)
	fields = append(fields, "disabled")
	rows, _, err := r.client.run(ctx, r.menu+"/print", "=.proplist="+strings.Join(fields, ","), "?list="+name)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rows, func(row map[string]string) bool { return row["list"] != name }), nil
}

// observed returns the enabled entries. A key that appears more than once, or a
// disabled entry, is left out so that the plan shows drift and an apply converges it.
func (r *containerList) observed(rows []map[string]string) entries {
	result, count := entries{}, map[string]int{}
	for _, row := range rows {
		count[row[r.key]]++
	}
	for _, row := range rows {
		if count[row[r.key]] > 1 || row["disabled"] == "true" || row["disabled"] == "yes" {
			continue
		}
		entry := map[string]string{}
		for _, field := range r.fields {
			entry[field] = row[field]
		}
		result[row[r.key]] = entry
	}
	return result
}

// converge removes, updates and adds entries until the list equals want.
func (r *containerList) converge(ctx context.Context, name string, want entries, rows []map[string]string) error {
	kept := map[string]bool{}
	for _, row := range rows {
		key := row[r.key]
		entry, wanted := want[key]
		if !wanted || kept[key] {
			if _, _, err := r.client.run(ctx, r.menu+"/remove", "=.id="+row[".id"]); err != nil {
				return err
			}
			continue
		}
		kept[key] = true
		words := []string{r.menu + "/set", "=.id=" + row[".id"]}
		for _, field := range r.fields {
			if row[field] != entry[field] {
				words = append(words, "="+field+"="+entry[field])
			}
		}
		if row["disabled"] == "true" || row["disabled"] == "yes" {
			words = append(words, "=disabled=no")
		}
		if len(words) > 2 {
			if _, _, err := r.client.run(ctx, words...); err != nil {
				return err
			}
		}
	}
	keys := make([]string, 0, len(want))
	for key := range want {
		if !kept[key] {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		words := []string{r.menu + "/add", "=list=" + name, "=" + r.key + "=" + key}
		for _, field := range r.fields {
			words = append(words, "="+field+"="+want[key][field])
		}
		if _, _, err := r.client.run(ctx, words...); err != nil {
			return err
		}
	}
	return nil
}

func (r *containerList) store(ctx context.Context, state *tfsdk.State, name string, values entries, diagnostics *diag.Diagnostics) {
	diagnostics.Append(state.SetAttribute(ctx, path.Root("id"), types.StringValue(name))...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("name"), types.StringValue(name))...)
	if r.attribute == "env" {
		elements := map[string]tfattr.Value{}
		for key, entry := range values {
			elements[key] = types.StringValue(entry["value"])
		}
		value, d := types.MapValue(types.StringType, elements)
		diagnostics.Append(d...)
		diagnostics.Append(state.SetAttribute(ctx, path.Root("env"), value)...)
		return
	}
	elements := map[string]tfattr.Value{}
	for dst, entry := range values {
		object, d := types.ObjectValue(mountEntryType.AttrTypes, map[string]tfattr.Value{
			"src": types.StringValue(entry["src"]), "mode": types.StringValue(entry["mode"]),
		})
		diagnostics.Append(d...)
		elements[dst] = object
	}
	value, d := types.MapValue(mountEntryType, elements)
	diagnostics.Append(d...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("mount"), value)...)
}

func (r *containerList) ready(diagnostics *diag.Diagnostics) bool {
	if r.client == nil {
		diagnostics.AddError("Managed RouterOS access required", "Use a provider configuration with ephemeral_access disabled.")
	}
	return r.client != nil
}

func (r *containerList) lock() func() {
	if r.menuLock == nil {
		return func() {}
	}
	r.menuLock.Lock()
	return r.menuLock.Unlock
}

func (r *containerList) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, creating bool, diagnostics *diag.Diagnostics) {
	if !r.ready(diagnostics) {
		return
	}
	var name types.String
	diagnostics.Append(plan.GetAttribute(ctx, path.Root("name"), &name)...)
	want, known := r.desired(ctx, plan, diagnostics)
	if diagnostics.HasError() {
		return
	}
	if !known {
		diagnostics.AddError("Unknown container list", "All entries must be known before writing the list.")
		return
	}
	defer r.lock()()
	rows, err := r.rows(ctx, name.ValueString())
	if err != nil {
		diagnostics.AddError("Read container list", err.Error())
		return
	}
	if creating && len(rows) > 0 {
		diagnostics.AddAttributeError(path.Root("name"), "Container list already exists",
			fmt.Sprintf("RouterOS already has %d entries in list %q. Import the list (terraform import with the list name) instead of creating it.", len(rows), name.ValueString()))
		return
	}
	if err := r.converge(ctx, name.ValueString(), want, rows); err != nil {
		diagnostics.AddError("Write container list", err.Error()+". Refresh and apply again to converge the remaining entries.")
		if creating {
			r.store(ctx, state, name.ValueString(), entries{}, diagnostics)
		}
		return
	}
	rows, err = r.rows(ctx, name.ValueString())
	if err != nil {
		diagnostics.AddError("Verify container list", err.Error())
		return
	}
	r.store(ctx, state, name.ValueString(), r.observed(rows), diagnostics)
}

func (r *containerList) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan, &resp.State, true, &resp.Diagnostics)
}

func (r *containerList) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.write(ctx, req.Plan, &resp.State, false, &resp.Diagnostics)
}

func (r *containerList) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer r.lock()()
	rows, err := r.rows(ctx, id.ValueString())
	switch {
	case err != nil:
		resp.Diagnostics.AddError("Read container list", err.Error())
	case len(rows) == 0:
		resp.State.RemoveResource(ctx)
	default:
		r.store(ctx, &resp.State, id.ValueString(), r.observed(rows), &resp.Diagnostics)
	}
}

func (r *containerList) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer r.lock()()
	rows, err := r.rows(ctx, id.ValueString())
	if err == nil {
		err = r.converge(ctx, id.ValueString(), entries{}, rows)
	}
	if err != nil {
		resp.Diagnostics.AddError("Delete container list", err.Error())
	}
}

func (r *containerList) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !listName.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid list name", "Import with the RouterOS list name, for example agent.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(req.ID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), types.StringValue(req.ID))...)
}
