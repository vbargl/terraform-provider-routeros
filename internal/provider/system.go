package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	ds "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Device mode and packages are observed only. RouterOS confirms device-mode changes
// physically (button or power cycle) and package changes need a reboot, so neither
// belongs in an API-driven resource.

func routerFlag(field, value string) (bool, error) {
	switch value {
	case "true", "yes":
		return true, nil
	case "false", "no":
		return false, nil
	}
	return false, fmt.Errorf("RouterOS returned an unexpected %s value", field)
}

type deviceMode struct{ client commandRunner }

type deviceModeValues struct {
	Mode      types.String `tfsdk:"mode"`
	Container types.Bool   `tfsdk:"container"`
	Flagged   types.Bool   `tfsdk:"flagged"`
}

func (*deviceMode) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device_mode"
}
func (*deviceMode) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = ds.Schema{
		Description: "Observe /system/device-mode. Changing device mode requires physical confirmation and is not managed.",
		Attributes: map[string]ds.Attribute{
			"mode":      ds.StringAttribute{Computed: true, Description: "Device mode, for example advanced or home."},
			"container": ds.BoolAttribute{Computed: true, Description: "Whether container mode is enabled."},
			"flagged":   ds.BoolAttribute{Computed: true, Description: "Whether RouterOS flagged the configuration and restricted the device."},
		},
	}
}
func (r *deviceMode) Configure(_ context.Context, req datasource.ConfigureRequest, _ *datasource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
}
func (r *deviceMode) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Managed RouterOS access required", "Use a provider configuration with ephemeral_access disabled.")
		return
	}
	rows, _, err := r.client.run(ctx, "/system/device-mode/print", "=.proplist=mode,container,flagged")
	if err != nil {
		resp.Diagnostics.AddError("Read device mode", err.Error())
		return
	}
	if len(rows) != 1 || rows[0]["mode"] == "" {
		resp.Diagnostics.AddError("Read device mode", "RouterOS did not return a device-mode record.")
		return
	}
	container, err := routerFlag("container", rows[0]["container"])
	if err != nil {
		resp.Diagnostics.AddError("Read device mode", err.Error())
		return
	}
	flagged, err := routerFlag("flagged", rows[0]["flagged"])
	if err != nil {
		resp.Diagnostics.AddError("Read device mode", err.Error())
		return
	}
	values := deviceModeValues{Mode: types.StringValue(rows[0]["mode"]), Container: types.BoolValue(container), Flagged: types.BoolValue(flagged)}
	resp.Diagnostics.Append(resp.State.Set(ctx, values)...)
}

type systemPackage struct{ client commandRunner }

type packageValues struct {
	Name      types.String `tfsdk:"name"`
	Installed types.Bool   `tfsdk:"installed"`
	Disabled  types.Bool   `tfsdk:"disabled"`
	Version   types.String `tfsdk:"version"`
	Scheduled types.String `tfsdk:"scheduled"`
}

var packageName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (*systemPackage) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package"
}
func (*systemPackage) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = ds.Schema{
		Description: "Observe one RouterOS package from /system/package. A missing package is reported as not installed.",
		Attributes: map[string]ds.Attribute{
			"name":      ds.StringAttribute{Required: true, Description: "Package name, for example container."},
			"installed": ds.BoolAttribute{Computed: true, Description: "Whether the package is installed, enabled or not."},
			"disabled":  ds.BoolAttribute{Computed: true, Description: "Whether an installed package is disabled. False when not installed."},
			"version":   ds.StringAttribute{Computed: true, Description: "Installed version; empty when not installed."},
			"scheduled": ds.StringAttribute{Computed: true, Description: "Pending action applied at the next reboot; empty when none."},
		},
	}
}
func (r *systemPackage) Configure(_ context.Context, req datasource.ConfigureRequest, _ *datasource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
}
func (r *systemPackage) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Managed RouterOS access required", "Use a provider configuration with ephemeral_access disabled.")
		return
	}
	var values packageValues
	resp.Diagnostics.Append(req.Config.Get(ctx, &values)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := values.Name.ValueString()
	if !packageName.MatchString(name) {
		resp.Diagnostics.AddError("Invalid package name", "Use a RouterOS package name such as container.")
		return
	}
	rows, _, err := r.client.run(ctx, "/system/package/print", "=.proplist=name,version,disabled,scheduled", "?name="+name)
	if err != nil {
		resp.Diagnostics.AddError("Read package", err.Error())
		return
	}
	values.Installed, values.Disabled = types.BoolValue(false), types.BoolValue(false)
	values.Version, values.Scheduled = types.StringValue(""), types.StringValue("")
	switch {
	case len(rows) > 1:
		resp.Diagnostics.AddError("Read package", "RouterOS returned more than one package with this name.")
		return
	case len(rows) == 1:
		if rows[0]["name"] != name {
			resp.Diagnostics.AddError("Read package", "RouterOS returned a different package.")
			return
		}
		disabled, err := routerFlag("disabled", rows[0]["disabled"])
		if err != nil {
			resp.Diagnostics.AddError("Read package", err.Error())
			return
		}
		values.Installed, values.Disabled = types.BoolValue(true), types.BoolValue(disabled)
		values.Version, values.Scheduled = types.StringValue(rows[0]["version"]), types.StringValue(rows[0]["scheduled"])
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, values)...)
}
