package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Container timing. Variables so tests can shorten them.
var (
	containerPollInterval   = 3 * time.Second
	containerExtractTimeout = 15 * time.Minute
	containerStateTimeout   = 2 * time.Minute
)

// Fields sent to /container. Changing any field not listed in containerInPlace
// replaces the container: RouterOS rejects most changes to a running container,
// and the intended workloads are stateless.
var containerFields = []string{
	"name", "remote-image", "file", "interface", "root-dir",
	"cmd", "entrypoint", "hostname", "user", "workdir", "dns",
	"logging", "restart-policy", "check-certificate", "start-on-boot", "comment",
}

// List references. RouterOS 7.25 accepts several comma-separated list names.
var containerLists = []string{"envlists", "mountlists"}

var containerDefaults = map[string]string{"logging": "no", "restart-policy": "no", "check-certificate": "yes", "start-on-boot": "no"}

var containerInPlace = map[string]bool{"start-on-boot": true, "comment": true}

var containerFlags = []string{"downloading/extracting", "deleting", "starting", "stopping", "running", "stopped"}

type container struct{ client commandRunner }

func (*container) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container"
}

func (*container) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	descriptions := map[string]string{
		"name":              "Container name.",
		"remote-image":      "Image reference pulled from the registry in routeros_container_config. Set exactly one of remote_image and file.",
		"file":              "Image tar file already on the router. Set exactly one of remote_image and file.",
		"interface":         "veth interface of the container.",
		"root-dir":          "Directory for the extracted image, for example /usb1/containers/name or a tmpfs disk. A leading slash is optional: RouterOS 7.25 stores the path with one, and both spellings are the same value.",
		"logging":           "yes or no: forward container output to the RouterOS log.",
		"restart-policy":    "no, on-failure or always.",
		"check-certificate": "yes or no: verify the registry TLS certificate.",
		"start-on-boot":     "yes or no. Changes in place.",
		"comment":           "Changes in place.",
	}
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"running": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("yes"),
			Description: "yes or no: desired run state. A stopped container that should run is started on the next apply."},
		"state": schema.StringAttribute{Computed: true,
			Description: "Observed state: extracting, starting, running, stopping, stopped, deleting or unknown."},
	}
	for _, field := range containerFields {
		modifiers := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
		switch {
		case field == "root-dir":
			modifiers = append(modifiers, stringplanmodifier.RequiresReplaceIf(rootDirChanged,
				"If the path changes other than by a leading slash, Terraform will destroy and recreate the resource.",
				"If the path changes other than by a leading slash, Terraform will destroy and recreate the resource."))
		case !containerInPlace[field]:
			modifiers = append(modifiers, stringplanmodifier.RequiresReplace())
		}
		attribute := schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: modifiers, Description: descriptions[field]}
		switch {
		case field == "name" || field == "interface" || field == "root-dir":
			attribute = schema.StringAttribute{Required: true, PlanModifiers: modifiers[1:], Description: descriptions[field]}
		case containerDefaults[field] != "":
			attribute.Default = stringdefault.StaticString(containerDefaults[field])
		}
		if attribute.Description == "" {
			attribute.Description = "Omit to leave the router's value unmanaged."
		}
		attributes[attr(field).String()] = attribute
	}
	for field, description := range map[string]string{
		"envlists":   "Names of routeros_container_env_list resources. Order is kept; a reordered list read from the router is not drift.",
		"mountlists": "Names of routeros_container_mount_list resources. Order is kept; a reordered list read from the router is not drift.",
	} {
		attributes[attr(field).String()] = schema.ListAttribute{
			ElementType: types.StringType, Optional: true, Computed: true, Description: description + " Omit to leave the router's value unmanaged.",
			PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown(), listplanmodifier.RequiresReplace()},
		}
	}
	resp.Schema = schema.Schema{
		Description: "Manage a RouterOS container. Requires container mode in /system/device-mode and the container package.",
		Attributes:  attributes,
	}
}

func (r *container) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
}

func (r *container) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	values := map[string]types.String{}
	for _, field := range append(containerFields, "running") {
		var value types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, attr(field), &value)...)
		values[field] = value
	}
	if resp.Diagnostics.HasError() {
		return
	}
	image, file := values["remote-image"], values["file"]
	if !image.IsUnknown() && !file.IsUnknown() && (image.ValueString() == "") == (file.ValueString() == "") {
		resp.Diagnostics.AddAttributeError(attr("remote-image"), "Image source required", "Set exactly one of remote_image and file.")
	}
	for _, field := range containerLists {
		var names types.List
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, attr(field), &names)...)
		if names.IsNull() || names.IsUnknown() {
			continue
		}
		seen := map[string]bool{}
		for _, element := range names.Elements() {
			name, ok := element.(types.String)
			if !ok || name.IsUnknown() {
				continue
			}
			value := name.ValueString()
			if !listName.MatchString(value) || seen[value] {
				resp.Diagnostics.AddAttributeError(attr(field), "Invalid list reference", "Use distinct, non-empty list names without commas or spaces.")
				break
			}
			seen[value] = true
		}
	}
	allowed := map[string][]string{
		"logging": {"yes", "no"}, "check-certificate": {"yes", "no"}, "start-on-boot": {"yes", "no"}, "running": {"yes", "no"},
		"restart-policy": {"no", "on-failure", "always"},
	}
	for field, options := range allowed {
		value := values[field]
		if !value.IsNull() && !value.IsUnknown() && !containsString(options, value.ValueString()) {
			resp.Diagnostics.AddAttributeError(attr(field), "Invalid container setting", "Use one of: "+strings.Join(options, ", ")+".")
		}
	}
	for _, field := range []string{"name", "interface", "root-dir"} {
		if value := values[field]; !value.IsUnknown() && value.ValueString() == "" {
			resp.Diagnostics.AddAttributeError(attr(field), "Empty required RouterOS setting", "Supply a value.")
		}
	}
}

func containsString(options []string, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

func containerState(row map[string]string) string {
	names := map[string]string{"downloading/extracting": "extracting"}
	for _, flag := range containerFlags {
		if row[flag] == "true" || row[flag] == "yes" {
			if name, ok := names[flag]; ok {
				return name
			}
			return flag
		}
	}
	return "unknown"
}

func normalizeContainer(field, value string) string {
	switch field {
	case "logging", "check-certificate", "start-on-boot":
		switch value {
		case "true", "yes":
			return "yes"
		case "false", "no", "":
			return "no"
		}
	}
	return value
}

// sameContainerValue reports whether a configured and a router value of field
// mean the same setting. RouterOS 7.25 returns root-dir with a leading slash
// even when it was added without one.
func sameContainerValue(field, a, b string) bool {
	a, b = normalizeContainer(field, a), normalizeContainer(field, b)
	if field == "root-dir" {
		a, b = strings.TrimPrefix(a, "/"), strings.TrimPrefix(b, "/")
	}
	return a == b
}

// rootDirChanged replaces the container only for a different path, not for a
// spelling with or without the leading slash, such as after an import.
func rootDirChanged(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	resp.RequiresReplace = !sameContainerValue("root-dir", req.StateValue.ValueString(), req.PlanValue.ValueString())
}

func (r *container) read(ctx context.Context, id string) (map[string]string, error) {
	if !objectID.MatchString(id) {
		return nil, errors.New("expected a RouterOS object ID")
	}
	fields := append(append(append([]string{".id"}, containerFields...), containerLists...), containerFlags...)
	rows, _, err := r.client.run(ctx, "/container/print", "=.proplist="+strings.Join(fields, ","), "?.id="+strings.ToUpper(id))
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if strings.EqualFold(row[".id"], id) {
			return row, nil
		}
	}
	return nil, nil
}

// wait polls until done reports true, the container disappears (gone) or the timeout passes.
func (r *container) wait(ctx context.Context, id string, timeout time.Duration, done func(state string) bool) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		row, err := r.read(ctx, id)
		if err != nil {
			return "", err
		}
		state := "gone"
		if row != nil {
			state = containerState(row)
		}
		if done(state) {
			return state, nil
		}
		if time.Now().After(deadline) {
			return state, fmt.Errorf("container %s is %s after %s", id, state, timeout)
		}
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		case <-time.After(containerPollInterval):
		}
	}
}

func (r *container) store(ctx context.Context, state *tfsdk.State, expected objectAttributes, row map[string]string, diagnostics *diag.Diagnostics) {
	diagnostics.Append(state.SetAttribute(ctx, path.Root("id"), types.StringValue(row[".id"]))...)
	for _, field := range containerFields {
		var prior types.String
		diagnostics.Append(expected.GetAttribute(ctx, attr(field), &prior)...)
		value := types.StringValue(normalizeContainer(field, row[field]))
		if !prior.IsNull() && !prior.IsUnknown() && sameContainerValue(field, value.ValueString(), prior.ValueString()) {
			value = prior
		}
		diagnostics.Append(state.SetAttribute(ctx, attr(field), value)...)
	}
	for _, field := range containerLists {
		var prior types.List
		diagnostics.Append(expected.GetAttribute(ctx, attr(field), &prior)...)
		names := splitList(row[field])
		value, d := types.ListValueFrom(ctx, types.StringType, names)
		diagnostics.Append(d...)
		if !prior.IsNull() && !prior.IsUnknown() {
			var wanted []string
			diagnostics.Append(prior.ElementsAs(ctx, &wanted, false)...)
			if sameNames(wanted, names) {
				value = prior
			}
		}
		diagnostics.Append(state.SetAttribute(ctx, attr(field), value)...)
	}
	observed := containerState(row)
	running := "no"
	if observed == "running" || observed == "starting" {
		running = "yes"
	}
	diagnostics.Append(state.SetAttribute(ctx, path.Root("running"), types.StringValue(running))...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("state"), types.StringValue(observed))...)
}

func (r *container) ready(diagnostics *diag.Diagnostics) bool {
	if r.client == nil {
		diagnostics.AddError("Managed RouterOS access required", "Use a provider configuration with ephemeral_access disabled.")
	}
	return r.client != nil
}

// setRunning starts or stops the container and waits for the result.
func (r *container) setRunning(ctx context.Context, id, desired string, diagnostics *diag.Diagnostics) {
	command, target := "/container/stop", "stopped"
	if desired == "yes" {
		command, target = "/container/start", "running"
	}
	if _, _, err := r.client.run(ctx, command, "=.id="+id); err != nil {
		diagnostics.AddError("Change container run state", err.Error())
		return
	}
	if _, err := r.wait(ctx, id, containerStateTimeout, func(state string) bool { return state == target }); err != nil {
		diagnostics.AddError("Change container run state", err.Error()+". Check /container/print and the RouterOS log on the router.")
	}
}

func (r *container) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	words := []string{"/container/add"}
	for _, field := range containerFields {
		var value types.String
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, attr(field), &value)...)
		if value.IsUnknown() || value.ValueString() == "" {
			continue // Unmanaged: RouterOS applies its default.
		}
		words = append(words, "="+field+"="+value.ValueString())
	}
	for _, field := range containerLists {
		var names types.List
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, attr(field), &names)...)
		if names.IsNull() || names.IsUnknown() || len(names.Elements()) == 0 {
			continue
		}
		var values []string
		resp.Diagnostics.Append(names.ElementsAs(ctx, &values, false)...)
		words = append(words, "="+field+"="+strings.Join(values, ","))
	}
	var running types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("running"), &running)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, id, err := r.client.run(ctx, words...)
	if err != nil {
		resp.Diagnostics.AddError("Create container", err.Error())
		return
	}
	if !objectID.MatchString(id) {
		resp.Diagnostics.AddError("Missing RouterOS object ID", "The command succeeded without returning a valid ID. Inspect /container on the router and import the container before retrying.")
		return
	}
	// Keep the binding even if extraction fails, so Terraform taints the container instead of losing it.
	resp.Diagnostics.Append(resp.State.Set(ctx, req.Plan.Raw)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(id))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("state"), types.StringValue("unknown"))...)
	for _, field := range containerFields {
		var value types.String
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, attr(field), &value)...)
		if value.IsUnknown() {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, attr(field), types.StringValue(""))...)
		}
	}
	for _, field := range containerLists {
		var names types.List
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, attr(field), &names)...)
		if names.IsUnknown() {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, attr(field), types.ListValueMust(types.StringType, nil))...)
		}
	}
	if _, err := r.wait(ctx, id, containerExtractTimeout, func(state string) bool { return state == "stopped" || state == "running" }); err != nil {
		resp.Diagnostics.AddError("Container image not ready", err.Error()+". The image pull or extraction did not finish. Check /container/print, free space in root_dir and the RouterOS log.")
		return
	}
	if running.ValueString() == "yes" {
		r.setRunning(ctx, id, "yes", &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	row, err := r.read(ctx, id)
	if err != nil || row == nil {
		resp.Diagnostics.AddWarning("Container was created but could not be verified", fmt.Sprintf("Container %s could not be read after creation. Refresh before applying again.", id))
		return
	}
	r.store(ctx, &resp.State, req.Plan, row, &resp.Diagnostics)
}

func (r *container) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	row, err := r.read(ctx, id.ValueString())
	switch {
	case err != nil:
		resp.Diagnostics.AddError("Read container", err.Error())
	case row == nil:
		resp.State.RemoveResource(ctx)
	default:
		r.store(ctx, &resp.State, req.State, row, &resp.Diagnostics)
	}
}

func (r *container) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.read(ctx, id.ValueString())
	if err != nil || current == nil {
		resp.Diagnostics.AddError("Update container", "The container could not be read. Refresh the plan.")
		return
	}
	words := []string{"/container/set", "=.id=" + current[".id"]}
	for field := range containerInPlace {
		var value types.String
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, attr(field), &value)...)
		if !value.IsUnknown() && !sameContainerValue(field, value.ValueString(), current[field]) {
			words = append(words, "="+field+"="+value.ValueString())
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if len(words) > 2 {
		if _, _, err := r.client.run(ctx, words...); err != nil {
			resp.Diagnostics.AddError("Update container", err.Error())
			return
		}
	}
	var running types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("running"), &running)...)
	observed := containerState(current)
	isRunning := observed == "running" || observed == "starting"
	if !resp.Diagnostics.HasError() && (running.ValueString() == "yes") != isRunning {
		r.setRunning(ctx, current[".id"], running.ValueString(), &resp.Diagnostics)
	}
	row, err := r.read(ctx, current[".id"])
	if err != nil || row == nil {
		resp.Diagnostics.AddError("Verify container", "The container could not be read after the update.")
		return
	}
	r.store(ctx, &resp.State, req.Plan, row, &resp.Diagnostics)
}

func (r *container) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	row, err := r.read(ctx, id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Delete container", err.Error())
		return
	}
	if row == nil {
		return
	}
	if state := containerState(row); state != "stopped" && state != "unknown" {
		if state != "stopping" {
			if _, _, err := r.client.run(ctx, "/container/stop", "=.id="+row[".id"]); err != nil {
				resp.Diagnostics.AddError("Stop container", err.Error())
				return
			}
		}
		if _, err := r.wait(ctx, row[".id"], containerStateTimeout, func(state string) bool { return state == "stopped" || state == "gone" }); err != nil {
			resp.Diagnostics.AddError("Stop container", err.Error())
			return
		}
	}
	if _, _, err := r.client.run(ctx, "/container/remove", "=.id="+row[".id"]); err != nil {
		resp.Diagnostics.AddError("Remove container", err.Error())
		return
	}
	if _, err := r.wait(ctx, row[".id"], containerStateTimeout, func(state string) bool { return state == "gone" }); err != nil {
		resp.Diagnostics.AddError("Remove container", err.Error())
	}
}

func (r *container) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !objectID.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid RouterOS object ID", "Use the RouterOS API ID, for example *1.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(strings.ToUpper(req.ID)))...)
}

// listName is a RouterOS env or mount list name usable in a comma-separated reference.
var listName = regexp.MustCompile(`^[^,\s]+$`)

func splitList(value string) []string {
	names := []string{}
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
