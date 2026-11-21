package provider

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type definition struct {
	name, menu                   string
	required, optional, computed []string
	ordered                      bool
}

var objects = []definition{
	{name: "wireguard_interface", menu: "/interface/wireguard", required: []string{"name", "listen-port", "mtu"}, optional: []string{"comment", "disabled=no"}, computed: []string{"public-key"}},
	{name: "wireguard_peer", menu: "/interface/wireguard/peers", required: []string{"interface", "public-key", "allowed-address"}, optional: []string{"endpoint-address", "endpoint-port=0", "persistent-keepalive=0s", "comment", "disabled=no"}},
	{name: "ip_address", menu: "/ip/address", required: []string{"address", "interface"}, optional: []string{"comment", "disabled=no"}},
	{name: "ip_route", menu: "/ip/route", required: []string{"dst-address", "gateway"}, optional: []string{"routing-table=main", "distance=1", "comment", "disabled=no"}},
	{name: "ip_pool", menu: "/ip/pool", required: []string{"name", "ranges"}, optional: []string{"comment"}},
	{name: "dhcp_server", menu: "/ip/dhcp-server", required: []string{"name", "interface", "address-pool", "lease-time"}, optional: []string{"comment", "disabled=no"}},
	{name: "dhcp_network", menu: "/ip/dhcp-server/network", required: []string{"address", "gateway"}, optional: []string{"dns-server", "domain", "comment"}},
	{name: "dhcp_lease", menu: "/ip/dhcp-server/lease", required: []string{"address", "mac-address", "server"}, optional: []string{"comment", "disabled=no"}},
	{name: "nat_rule", menu: "/ip/firewall/nat", required: []string{"chain", "action", "src-address", "dst-address", "to-addresses"}, optional: []string{"in-interface", "out-interface", "comment", "disabled=yes"}, ordered: true},
	{name: "firewall_rule", menu: "/ip/firewall/filter", required: []string{"chain", "action", "src-address", "dst-address"}, optional: []string{"protocol", "dst-port", "in-interface", "out-interface", "comment", "disabled=yes"}, ordered: true},
	{name: "interface_veth", menu: "/interface/veth", required: []string{"name", "address"}, optional: []string{"gateway", "comment", "disabled=no"}},
	{name: "container_env", menu: "/container/envs", required: []string{"list", "key", "value"}, optional: []string{"comment", "disabled=no"}},
	{name: "container_mount", menu: "/container/mounts", required: []string{"list", "src", "dst", "mode"}, optional: []string{"comment", "disabled=no"}},
}

type managedObject struct {
	definition
	client   commandRunner
	menuLock *sync.Mutex
}

var (
	objectID = regexp.MustCompile(`^\*[0-9A-Fa-f]+$`)
	envKey   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func attr(field string) path.Path { return path.Root(strings.ReplaceAll(field, "-", "_")) }

func (r *managedObject) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.name
}

func (r *managedObject) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
	}
	for _, field := range r.required {
		attributes[attr(field).String()] = schema.StringAttribute{Required: true}
	}
	for _, entry := range r.optional {
		field, defaultValue, _ := strings.Cut(entry, "=")
		attributes[attr(field).String()] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(defaultValue)}
	}
	for _, field := range r.computed {
		attributes[attr(field).String()] = schema.StringAttribute{
			Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	if r.ordered {
		attributes["place_before"] = schema.StringAttribute{Optional: true, Description: "ID of a rule that must appear later in the same menu. Omit to leave position unmanaged."}
	}
	resp.Schema = schema.Schema{Attributes: attributes}
}

func (r *managedObject) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
	if client, ok := req.ProviderData.(*routerClient); ok {
		r.menuLock = orderedMenuLock(client.address, r.menu)
	}
}

func (r *managedObject) fields() []string {
	fields := append([]string{}, r.required...)
	for _, entry := range r.optional {
		field, _, _ := strings.Cut(entry, "=")
		fields = append(fields, field)
	}
	return fields
}

func (r *managedObject) read(ctx context.Context, id, anchor string) (map[string]string, error) {
	if !objectID.MatchString(id) {
		return nil, errors.New("expected a RouterOS object ID")
	}
	fields := append([]string{".id"}, r.fields()...)
	fields = append(fields, r.computed...)
	if r.name == "dhcp_lease" || r.name == "ip_address" || r.name == "ip_route" || r.ordered {
		fields = append(fields, "dynamic")
	}
	words := []string{r.menu + "/print", "=.proplist=" + strings.Join(fields, ",")}
	if !r.ordered {
		words = append(words, "?.id="+strings.ToUpper(id))
	}
	rows, _, err := r.client.run(ctx, words...)
	if err != nil {
		return nil, err
	}
	for index, row := range rows {
		if !strings.EqualFold(row[".id"], id) {
			continue
		}
		if row["dynamic"] == "true" || row["dynamic"] == "yes" {
			return nil, errors.New("dynamic RouterOS objects cannot be managed")
		}
		if r.name == "wireguard_interface" && row["public-key"] == "" {
			return nil, errors.New("RouterOS did not expose the public identity")
		}
		if r.ordered && anchor != "" {
			delete(row, "place-before")
			for _, later := range rows[index+1:] {
				if strings.EqualFold(later[".id"], anchor) {
					row["place-before"] = later[".id"]
					break
				}
			}
		}
		return row, nil
	}
	return nil, nil
}

type objectAttributes interface {
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}

func (r *managedObject) store(ctx context.Context, state *tfsdk.State, expected objectAttributes, row map[string]string, diagnostics *diag.Diagnostics) {
	diagnostics.Append(state.SetAttribute(ctx, path.Root("id"), types.StringValue(row[".id"]))...)
	fields := append(r.fields(), r.computed...)
	if r.ordered {
		fields = append(fields, "place-before")
	}
	for _, field := range fields {
		var prior types.String
		diagnostics.Append(expected.GetAttribute(ctx, attr(field), &prior)...)
		value := types.StringValue(r.normalize(field, row[field]))
		switch {
		case field == "place-before" && row[field] == "":
			value = types.StringNull()
		case !prior.IsNull() && !prior.IsUnknown() && value.ValueString() == r.normalize(field, prior.ValueString()):
			value = prior
		}
		diagnostics.Append(state.SetAttribute(ctx, attr(field), value)...)
	}
}

func (r *managedObject) ready(diagnostics *diag.Diagnostics) bool {
	if r.client != nil {
		return true
	}
	diagnostics.AddError("Managed RouterOS access required", "Use a provider configuration with ephemeral_access disabled.")
	return false
}

func (r *managedObject) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var anchor types.String
	if r.ordered {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, attr("place-before"), &anchor)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if r.menuLock != nil {
		r.menuLock.Lock()
		defer r.menuLock.Unlock()
	}
	row, err := r.read(ctx, id.ValueString(), anchor.ValueString())
	switch {
	case err != nil:
		resp.Diagnostics.AddError("Read RouterOS object", err.Error())
	case row == nil:
		resp.State.RemoveResource(ctx)
	default:
		r.store(ctx, &resp.State, req.State, row, &resp.Diagnostics)
	}
}

func (r *managedObject) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, id string, diagnostics *diag.Diagnostics) {
	if !r.ready(diagnostics) {
		return
	}
	if r.menuLock != nil {
		r.menuLock.Lock()
		defer r.menuLock.Unlock()
	}
	var anchor types.String
	if r.ordered {
		diagnostics.Append(plan.GetAttribute(ctx, attr("place-before"), &anchor)...)
		if anchor.IsUnknown() || (!anchor.IsNull() && (!objectID.MatchString(anchor.ValueString()) || strings.EqualFold(anchor.ValueString(), id))) {
			diagnostics.AddError("Invalid rule anchor", "Supply the RouterOS object ID of another rule in the same menu.")
		}
	}
	if diagnostics.HasError() {
		return
	}
	command := "add"
	var current map[string]string
	if id != "" {
		var err error
		current, err = r.read(ctx, id, anchor.ValueString())
		if err != nil {
			diagnostics.AddError("Check RouterOS object", err.Error())
			return
		}
		if current == nil {
			diagnostics.AddError("Update RouterOS object", "The object disappeared. Refresh the plan.")
			return
		}
		command = "set"
	}
	words := []string{r.menu + "/" + command}
	if id != "" {
		words = append(words, "=.id="+id)
	}
	values := make(map[string]string)
	for _, field := range r.fields() {
		var value types.String
		diagnostics.Append(plan.GetAttribute(ctx, attr(field), &value)...)
		if value.IsUnknown() {
			diagnostics.AddError("Unknown RouterOS setting", fmt.Sprintf("The value of %s must be known before writing the object.", attr(field)))
		}
		values[field] = value.ValueString()
		switch {
		case command == "add" && value.ValueString() == "":
			// Let RouterOS apply its defaults for omitted empty attributes.
		case command == "set" && r.normalize(field, current[field]) == r.normalize(field, value.ValueString()):
			// Send only changes, including an empty value to clear a setting.
		default:
			words = append(words, "="+field+"="+value.ValueString())
		}
	}
	r.validateValues(values, nil, diagnostics)
	if r.name == "nat_rule" && !diagnostics.HasError() {
		vpnInterface := values["out-interface"]
		if values["chain"] == "dstnat" {
			vpnInterface = values["in-interface"]
		}
		rows, _, err := r.client.run(ctx, "/interface/wireguard/print", "=.proplist=name", "?name="+vpnInterface)
		if err != nil || len(rows) != 1 || rows[0]["name"] != vpnInterface {
			diagnostics.AddError("WireGuard scope unavailable", "The selected VPN interface must be a readable WireGuard interface on this router.")
		}
	}

	if !diagnostics.HasError() {
		r.rejectDuplicateListEntry(ctx, id, values, diagnostics)
	}
	if command == "add" && !anchor.IsNull() && anchor.ValueString() != "" {
		words = append(words, "=place-before="+strings.ToUpper(anchor.ValueString()))
	}
	if diagnostics.HasError() {
		return
	}
	needsMove := r.ordered && command == "set" && !anchor.IsNull() && current["place-before"] == ""
	move := func() bool {
		_, _, err := r.client.run(ctx, r.menu+"/move", "=numbers="+id, "=destination="+strings.ToUpper(anchor.ValueString()))
		if err != nil {
			diagnostics.AddError("Move RouterOS rule", err.Error())
		}
		return err == nil
	}
	// Move a rule that should be enabled before applying its settings. A failed
	// move must not enable a rule at the old position.
	if needsMove && values["disabled"] == "no" {
		if !move() {
			return
		}
		position, err := r.read(ctx, id, anchor.ValueString())
		if err != nil || position["place-before"] == "" {
			diagnostics.AddError("Verify RouterOS rule position", "The rule's position could not be verified. Rule settings were left untouched.")
			return
		}
	}
	var createdID string
	if command == "add" || len(words) > 2 {
		var err error
		_, createdID, err = r.client.run(ctx, words...)
		if err != nil {
			diagnostics.AddError("Write RouterOS object", err.Error())
			return
		}
	}
	if command == "add" {
		if !objectID.MatchString(createdID) {
			diagnostics.AddError("Missing RouterOS object ID", "The command succeeded without returning a valid ID. Inspect the router and import the object before retrying.")
			return
		}
		id = createdID
		// Keep a known binding if the subsequent read fails.
		diagnostics.Append(state.Set(ctx, plan.Raw)...)
		for _, field := range r.computed {
			diagnostics.Append(state.SetAttribute(ctx, attr(field), types.StringNull())...)
		}
	}
	diagnostics.Append(state.SetAttribute(ctx, path.Root("id"), types.StringValue(id))...)
	// A rule that should be disabled can only move after it is disabled.
	if needsMove && values["disabled"] == "yes" && !move() {
		return
	}
	row, err := r.read(ctx, id, anchor.ValueString())
	if err != nil || row == nil {
		detail := fmt.Sprintf("Object %s could not be read after the write.", id)
		if err != nil {
			detail += " " + err.Error()
		}
		if command == "add" {
			detail += " Its ID was retained. Refresh before applying again."
			if r.name == "wireguard_interface" {
				detail += " The public_key output is null until refresh succeeds. Do not deploy dependent peer configuration from this result."
			}
			diagnostics.AddWarning("RouterOS object was created but could not be verified", detail)
		} else {
			diagnostics.AddError("Verify RouterOS object", detail)
		}
		return
	}
	r.store(ctx, state, plan, row, diagnostics)
}

func (r *managedObject) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan, &resp.State, "", &resp.Diagnostics)
}
func (r *managedObject) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if !resp.Diagnostics.HasError() {
		r.write(ctx, req.Plan, &resp.State, id.ValueString(), &resp.Diagnostics)
	}
}
func (r *managedObject) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.menuLock != nil {
		r.menuLock.Lock()
		defer r.menuLock.Unlock()
	}
	row, err := r.read(ctx, id.ValueString(), "")
	if err == nil && row != nil {
		_, _, err = r.client.run(ctx, r.menu+"/remove", "=.id="+row[".id"])
	}
	if err != nil {
		resp.Diagnostics.AddError("Delete RouterOS object", err.Error())
	}
}
func (r *managedObject) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !objectID.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid RouterOS object ID", "Use the RouterOS API ID, for example *A.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(strings.ToUpper(req.ID)))...)
}

func (r *managedObject) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	values := make(map[string]string)
	unknown := make(map[string]bool)
	for _, field := range r.fields() {
		var value types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, attr(field), &value)...)
		switch {
		case value.IsUnknown():
			unknown[field] = true
		case !value.IsNull():
			values[field] = value.ValueString()
		}
	}
	if r.ordered {
		var anchor types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, attr("place-before"), &anchor)...)
		if !anchor.IsNull() && !anchor.IsUnknown() && !objectID.MatchString(anchor.ValueString()) {
			resp.Diagnostics.AddAttributeError(attr("place-before"), "Invalid rule anchor", "Supply a RouterOS object ID or omit this attribute.")
		}
	}
	if !resp.Diagnostics.HasError() {
		r.validateValues(values, unknown, &resp.Diagnostics)
	}
}

func (r *managedObject) validateValues(values map[string]string, unknown map[string]bool, diagnostics *diag.Diagnostics) {
	for _, field := range r.required {
		if value, known := values[field]; known && value == "" {
			diagnostics.AddAttributeError(attr(field), "Empty required RouterOS setting", "Supply a value.")
		}
	}
	for _, entry := range r.optional {
		field, defaultValue, _ := strings.Cut(entry, "=")
		if value, known := values[field]; known && value == "" && defaultValue != "" {
			diagnostics.AddAttributeError(attr(field), "Empty RouterOS setting", "Supply a value or omit the attribute to use its default.")
		}
	}
	if value, known := values["disabled"]; known && value != "" && value != "yes" && value != "no" {
		diagnostics.AddAttributeError(attr("disabled"), "Invalid disabled value", "Use yes or no.")
	}
	switch r.name {
	case "container_mount":
		if list, known := values["list"]; known && list != "" && !listName.MatchString(list) {
			diagnostics.AddAttributeError(attr("list"), "Invalid list name", "Use a list name without commas or spaces; containers reference several lists as a comma-separated value.")
		}
		if mode, known := values["mode"]; known && !slices.Contains([]string{"ro", "ro,noexec", "rw", "rw,noexec"}, mode) {
			diagnostics.AddAttributeError(attr("mode"), "Invalid mount mode", "Use ro, ro,noexec, rw or rw,noexec.")
		}
		for _, field := range []string{"src", "dst"} {
			if value, known := values[field]; known && value != "" && !strings.HasPrefix(value, "/") {
				diagnostics.AddAttributeError(attr(field), "Invalid mount path", "Use an absolute path.")
			}
		}
	case "container_env":
		if list, known := values["list"]; known && list != "" && !listName.MatchString(list) {
			diagnostics.AddAttributeError(attr("list"), "Invalid list name", "Use a list name without commas or spaces; containers reference several lists as a comma-separated value.")
		}
		if key, known := values["key"]; known && key != "" && !envKey.MatchString(key) {
			diagnostics.AddAttributeError(attr("key"), "Invalid environment variable name", "Use letters, digits and underscores, not starting with a digit.")
		}
	case "interface_veth":
		if address, known := values["address"]; known && address != "" {
			for _, entry := range strings.Split(address, ",") {
				if _, err := netip.ParsePrefix(strings.TrimSpace(entry)); err != nil {
					diagnostics.AddAttributeError(attr("address"), "Invalid veth address", "Use one or more comma-separated prefixes, for example 192.168.88.20/24.")
					break
				}
			}
		}
		if gateway, known := values["gateway"]; known && gateway != "" {
			if addr, err := netip.ParseAddr(gateway); err != nil || !addr.Is4() {
				diagnostics.AddAttributeError(attr("gateway"), "Invalid veth gateway", "Use an IPv4 address.")
			}
		}
	}
	if !r.ordered {
		return
	}
	for _, field := range []string{"src-address", "dst-address"} {
		if value, known := values[field]; known {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || !prefix.Addr().Is4() || (r.name == "nat_rule" && prefix.Bits() == 0) {
				diagnostics.AddAttributeError(attr(field), "Invalid rule match", "Supply an explicit IPv4 prefix. NAT requires a prefix narrower than /0.")
			}
		}
	}
	if r.name == "nat_rule" {
		if value, known := values["to-addresses"]; known {
			target, err := netip.ParseAddr(value)
			if err != nil || !target.Is4() {
				diagnostics.AddAttributeError(attr("to-addresses"), "Invalid NAT target", "Supply a single IPv4 address.")
			}
		}
		if action, known := values["action"]; known && action != "src-nat" && action != "dst-nat" && action != "netmap" {
			diagnostics.AddAttributeError(attr("action"), "Unsupported NAT action", "Use src-nat, dst-nat or netmap.")
		}
		if chain, known := values["chain"]; known {
			switch chain {
			case "srcnat", "dstnat":
				interfaceField, action := "out-interface", "src-nat"
				if chain == "dstnat" {
					interfaceField, action = "in-interface", "dst-nat"
				}
				if configured, exists := values["action"]; exists && configured != action && configured != "netmap" {
					diagnostics.AddAttributeError(attr("action"), "NAT action does not match the chain", "Use "+action+" or netmap.")
				}
				if !unknown[interfaceField] && values[interfaceField] == "" {
					diagnostics.AddAttributeError(attr(interfaceField), "Missing WireGuard scope", "Specify the WireGuard interface in the direction of the chain.")
				}
			default:
				diagnostics.AddAttributeError(attr("chain"), "Unsupported NAT chain", "Use srcnat or dstnat.")
			}
		}
	}
	if r.name == "firewall_rule" {
		if action, known := values["action"]; known && action != "accept" && action != "drop" && action != "reject" {
			diagnostics.AddAttributeError(attr("action"), "Unsupported filter action", "Use accept, drop or reject.")
		}
		if port := values["dst-port"]; port != "" && !unknown["protocol"] && values["protocol"] != "tcp" && values["protocol"] != "udp" {
			diagnostics.AddAttributeError(attr("protocol"), "Invalid filter protocol", "Use tcp or udp when matching a destination port.")
		}
	}
}

// listUniqueField is the field that must be unique within one container env or mount list.
var listUniqueField = map[string]string{"container_env": "key", "container_mount": "dst"}

// rejectDuplicateListEntry refuses a second entry with the same key (env) or
// destination (mount) in one list. The container would otherwise get an
// ambiguous environment or overlapping mounts depending on RouterOS ordering.
func (r *managedObject) rejectDuplicateListEntry(ctx context.Context, id string, values map[string]string, diagnostics *diag.Diagnostics) {
	field, ok := listUniqueField[r.name]
	if !ok {
		return
	}
	rows, _, err := r.client.run(ctx, r.menu+"/print", "=.proplist=.id,list,"+field, "?list="+values["list"])
	if err != nil {
		diagnostics.AddError("Check container list", err.Error())
		return
	}
	for _, row := range rows {
		if row["list"] == values["list"] && row[field] == values[field] && !strings.EqualFold(row[".id"], id) {
			diagnostics.AddAttributeError(attr(field), "Duplicate container list entry",
				fmt.Sprintf("List %q already has an entry with %s %q (RouterOS object %s). Use one entry per %s in a list.", values["list"], field, values[field], row[".id"], field))
			return
		}
	}
}
