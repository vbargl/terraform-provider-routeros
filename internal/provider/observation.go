package provider

import (
	"context"
	"encoding/base64"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	ds "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	es "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type wireguardKey struct{ client commandRunner }

type keyValues struct {
	InterfaceID types.String `tfsdk:"interface_id"`
	PrivateKey  types.String `tfsdk:"private_key"`
}

func (*wireguardKey) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wireguard_private_key"
}
func (*wireguardKey) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = es.Schema{Attributes: map[string]es.Attribute{
		"interface_id": es.StringAttribute{Required: true},
		"private_key":  es.StringAttribute{Computed: true, Sensitive: true},
	}}
}
func (r *wireguardKey) Configure(_ context.Context, req ephemeral.ConfigureRequest, _ *ephemeral.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
}
func (r *wireguardKey) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Dedicated key access required", "Select an aliased provider configuration with ephemeral_access enabled.")
		return
	}
	var values keyValues
	resp.Diagnostics.Append(req.Config.Get(ctx, &values)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := values.InterfaceID.ValueString()
	if !objectID.MatchString(id) {
		resp.Diagnostics.AddError("Invalid WireGuard interface ID", "Supply a RouterOS object ID.")
		return
	}
	rows, _, err := r.client.run(ctx, "/interface/wireguard/print", "=.proplist=.id,private-key", "?.id="+id)
	if err != nil {
		resp.Diagnostics.AddError("Read WireGuard private key", err.Error())
		return
	}
	if len(rows) != 1 || rows[0][".id"] != id {
		resp.Diagnostics.AddError("WireGuard interface unavailable", "The requested interface could not be found.")
		return
	}
	key := rows[0]["private-key"]
	decoded, err := base64.StdEncoding.DecodeString(key)
	valid := err == nil && len(decoded) == 32
	clear(decoded)
	if !valid {
		resp.Diagnostics.AddError("WireGuard key unavailable", "Check the dedicated account's sensitive permission.")
		return
	}
	values.PrivateKey = types.StringValue(key)
	resp.Diagnostics.Append(resp.Result.Set(ctx, values)...)
}

type dynamicLeases struct{ client commandRunner }

type lease struct {
	ID      types.String `tfsdk:"id"`
	Address types.String `tfsdk:"address"`
	MAC     types.String `tfsdk:"mac_address"`
	Server  types.String `tfsdk:"server"`
	Status  types.String `tfsdk:"status"`
}
type leaseValues struct {
	Leases []lease `tfsdk:"leases"`
}

func (*dynamicLeases) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_dynamic_leases"
}
func (*dynamicLeases) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	fields := map[string]ds.Attribute{}
	for _, name := range []string{"id", "address", "mac_address", "server", "status"} {
		fields[name] = ds.StringAttribute{Computed: true}
	}
	resp.Schema = ds.Schema{Attributes: map[string]ds.Attribute{
		"leases": ds.ListNestedAttribute{Computed: true, NestedObject: ds.NestedAttributeObject{Attributes: fields}},
	}}
}
func (r *dynamicLeases) Configure(_ context.Context, req datasource.ConfigureRequest, _ *datasource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
}
func (r *dynamicLeases) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Managed RouterOS access required", "Use a provider configuration with ephemeral_access disabled.")
		return
	}
	rows, _, err := r.client.run(ctx, "/ip/dhcp-server/lease/print", "=.proplist=.id,address,mac-address,server,status,dynamic")
	if err != nil {
		resp.Diagnostics.AddError("Read dynamic leases", err.Error())
		return
	}
	values := leaseValues{Leases: make([]lease, 0, len(rows))}
	for _, row := range rows {
		if row["dynamic"] == "true" || row["dynamic"] == "yes" {
			values.Leases = append(values.Leases, lease{
				ID: types.StringValue(row[".id"]), Address: types.StringValue(row["address"]),
				MAC: types.StringValue(row["mac-address"]), Server: types.StringValue(row["server"]), Status: types.StringValue(row["status"]),
			})
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, values)...)
}
