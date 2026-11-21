package provider

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

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
}

type managedObject struct {
	definition
	client commandRunner
}

var objectID = regexp.MustCompile(`^\*[0-9A-Fa-f]+$`)

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
		attributes[attr(field).String()] = schema.StringAttribute{Computed: true}
	}
	if r.ordered {
		attributes["place_before"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "ID of the immediate successor. Empty appends to the end."}
	}
	resp.Schema = schema.Schema{Attributes: attributes}
}

func (r *managedObject) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
}

func (r *managedObject) fields() []string {
	fields := append([]string{}, r.required...)
	for _, entry := range r.optional {
		field, _, _ := strings.Cut(entry, "=")
		fields = append(fields, field)
	}
	return fields
}

func (r *managedObject) read(ctx context.Context, id string) (map[string]string, error) {
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
		words = append(words, "?.id="+id)
	}
	rows, _, err := r.client.run(ctx, words...)
	if err != nil {
		return nil, err
	}
	for index, row := range rows {
		if row[".id"] != id {
			continue
		}
		if row["dynamic"] == "true" || row["dynamic"] == "yes" {
			return nil, errors.New("dynamic RouterOS objects cannot be managed")
		}
		if r.name == "wireguard_interface" && row["public-key"] == "" {
			return nil, errors.New("RouterOS did not expose the public identity")
		}
		if r.ordered && index+1 < len(rows) {
			row["place-before"] = rows[index+1][".id"]
		}
		return row, nil
	}
	return nil, nil
}

func (r *managedObject) store(ctx context.Context, state *tfsdk.State, row map[string]string, diagnostics *diag.Diagnostics) {
	diagnostics.Append(state.SetAttribute(ctx, path.Root("id"), types.StringValue(row[".id"]))...)
	fields := append(r.fields(), r.computed...)
	if r.ordered {
		fields = append(fields, "place-before")
	}
	for _, field := range fields {
		value := row[field]
		if r.ordered && (field == "src-address" || field == "dst-address") {
			if address, err := netip.ParseAddr(value); err == nil && address.Is4() {
				value = netip.PrefixFrom(address, 32).String()
			}
		}
		if field == "disabled" {
			switch value {
			case "true", "yes":
				value = "yes"
			case "false", "no", "":
				value = "no"
			}
		}
		diagnostics.Append(state.SetAttribute(ctx, attr(field), types.StringValue(value))...)
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
	row, err := r.read(ctx, id.ValueString())
	switch {
	case err != nil:
		resp.Diagnostics.AddError("Read RouterOS object", err.Error())
	case row == nil:
		resp.State.RemoveResource(ctx)
	default:
		r.store(ctx, &resp.State, row, &resp.Diagnostics)
	}
}

func (r *managedObject) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, id string, diagnostics *diag.Diagnostics) {
	if !r.ready(diagnostics) {
		return
	}
	command := "add"
	var current map[string]string
	if id != "" {
		var err error
		current, err = r.read(ctx, id)
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
		values[field] = value.ValueString()
		words = append(words, "="+field+"="+value.ValueString())
	}
	if r.ordered {
		for _, field := range []string{"src-address", "dst-address"} {
			prefix, err := netip.ParsePrefix(values[field])
			if err != nil || !prefix.Addr().Is4() || (r.name == "nat_rule" && prefix.Bits() == 0) {
				diagnostics.AddError("Invalid rule match", "Supply explicit IPv4 source and destination prefixes. NAT requires prefixes narrower than /0.")
			}
		}
	}
	if r.name == "nat_rule" && !diagnostics.HasError() {
		vpnInterface := values["out-interface"]
		target, err := netip.ParseAddr(values["to-addresses"])
		valid := err == nil && target.Is4()
		switch values["chain"] {
		case "srcnat":
			valid = valid && (values["action"] == "src-nat" || values["action"] == "netmap")
		case "dstnat":
			valid = valid && (values["action"] == "dst-nat" || values["action"] == "netmap")
			vpnInterface = values["in-interface"]
		default:
			valid = false
		}
		if !valid || vpnInterface == "" {
			diagnostics.AddError("Invalid scoped NAT rule", "Supply a single IPv4 target, a matching NAT action and the VPN interface in the direction of the chain.")
		} else {
			rows, _, err := r.client.run(ctx, "/interface/wireguard/print", "=.proplist=name", "?name="+vpnInterface)
			if err != nil || len(rows) != 1 || rows[0]["name"] != vpnInterface {
				diagnostics.AddError("WireGuard scope unavailable", "The selected VPN interface must be a readable WireGuard interface on this router.")
			}
		}
	}
	if r.name == "firewall_rule" {
		switch values["action"] {
		case "accept", "drop", "reject":
		default:
			diagnostics.AddError("Unsupported filter action", "Use accept, drop or reject.")
		}
		if values["chain"] == "" || (values["dst-port"] != "" && values["protocol"] != "tcp" && values["protocol"] != "udp") {
			diagnostics.AddError("Invalid filter rule", "Specify a chain and use tcp or udp when matching a destination port.")
		}
	}

	var anchor types.String
	if r.ordered {
		diagnostics.Append(plan.GetAttribute(ctx, attr("place-before"), &anchor)...)
		if !anchor.IsNull() && anchor.ValueString() != "" && (!objectID.MatchString(anchor.ValueString()) || anchor.ValueString() == id) {
			diagnostics.AddError("Invalid rule successor", "The successor must be another rule's RouterOS object ID.")
		}
		if command == "add" && anchor.ValueString() != "" {
			words = append(words, "=place-before="+anchor.ValueString())
		}
	}
	if diagnostics.HasError() {
		return
	}
	_, createdID, err := r.client.run(ctx, words...)
	if err != nil {
		diagnostics.AddError("Write RouterOS object", err.Error())
		return
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
	if r.ordered && command == "set" && current["place-before"] != anchor.ValueString() {
		move := []string{r.menu + "/move", "=numbers=" + id}
		if anchor.ValueString() != "" {
			move = append(move, "=destination="+anchor.ValueString())
		}
		if _, _, err := r.client.run(ctx, move...); err != nil {
			diagnostics.AddError("Move RouterOS rule", err.Error())
			return
		}
	}
	row, err := r.read(ctx, id)
	if err != nil {
		diagnostics.AddError("Verify RouterOS object", err.Error())
		return
	}
	if row == nil {
		diagnostics.AddError("Verify RouterOS object", fmt.Sprintf("Object %s could not be read after the write.", id))
		return
	}
	r.store(ctx, state, row, diagnostics)
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
	row, err := r.read(ctx, id.ValueString())
	if err == nil && row != nil {
		_, _, err = r.client.run(ctx, r.menu+"/remove", "=.id="+id.ValueString())
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
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
