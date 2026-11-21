package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const containerConfigID = "container-config"

// The registry password is never read back and never stored: it is a write-only
// attribute, sent only on create or when password_wo_version changes.
var containerConfigFields = []string{"registry-url", "username", "tmpdir", "layer-dir"}

type containerConfig struct{ client commandRunner }

func (*containerConfig) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_config"
}

func (*containerConfig) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	descriptions := map[string]string{
		"registry-url": "Registry for remote_image, for example https://registry.example.net.",
		"username":     "Registry user.",
		"tmpdir":       "Directory for image downloads, for example a tmpfs disk on routers with little flash.",
		"layer-dir":    "Directory for shared image layers.",
	}
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"password_wo": schema.StringAttribute{Optional: true, Sensitive: true, WriteOnly: true,
			Description: "Registry password or token. Write-only: never stored in plan or state. Sent on create and whenever password_wo_version changes."},
		"password_wo_version": schema.StringAttribute{Optional: true,
			Description: "Change this value to send password_wo again, for example after rotating the token."},
	}
	for _, field := range containerConfigFields {
		attributes[attr(field).String()] = schema.StringAttribute{
			Optional: true, Computed: true, Description: descriptions[field] + " Omit to leave the router's value unmanaged.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	resp.Schema = schema.Schema{
		Description: "Manage the /container/config singleton. Destroying it only removes it from state; the router keeps its settings.",
		Attributes:  attributes,
	}
}

func (r *containerConfig) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.client, _ = req.ProviderData.(commandRunner)
}

func (r *containerConfig) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var password, version types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("password_wo"), &password)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("password_wo_version"), &version)...)
	if !password.IsNull() && version.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("password_wo_version"), "Password version required",
			"Set password_wo_version with password_wo so that a changed password can be sent again.")
	}
}

func (r *containerConfig) ready(diagnostics *diag.Diagnostics) bool {
	if r.client == nil {
		diagnostics.AddError("Managed RouterOS access required", "Use a provider configuration with ephemeral_access disabled.")
	}
	return r.client != nil
}

func (r *containerConfig) read(ctx context.Context) (map[string]string, error) {
	rows, _, err := r.client.run(ctx, "/container/config/print", "=.proplist=registry-url,username,tmpdir,layer-dir")
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errors.New("RouterOS did not return the container configuration; check that the container package is enabled")
	}
	return rows[0], nil
}

func (r *containerConfig) store(ctx context.Context, state *tfsdk.State, expected objectAttributes, row map[string]string, diagnostics *diag.Diagnostics) {
	diagnostics.Append(state.SetAttribute(ctx, path.Root("id"), types.StringValue(containerConfigID))...)
	for _, field := range containerConfigFields {
		diagnostics.Append(state.SetAttribute(ctx, attr(field), types.StringValue(row[field]))...)
	}
	var version types.String
	diagnostics.Append(expected.GetAttribute(ctx, path.Root("password_wo_version"), &version)...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("password_wo_version"), version)...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root("password_wo"), types.StringNull())...)
}

func (r *containerConfig) write(ctx context.Context, plan tfsdk.Plan, config tfsdk.Config, prior *tfsdk.State, state *tfsdk.State, diagnostics *diag.Diagnostics) {
	if !r.ready(diagnostics) {
		return
	}
	current, err := r.read(ctx)
	if err != nil {
		diagnostics.AddError("Read container configuration", err.Error())
		return
	}
	words := []string{"/container/config/set"}
	for _, field := range containerConfigFields {
		var value types.String
		diagnostics.Append(plan.GetAttribute(ctx, attr(field), &value)...)
		if !value.IsUnknown() && !value.IsNull() && value.ValueString() != current[field] {
			words = append(words, "="+field+"="+value.ValueString())
		}
	}
	var password, version, priorVersion types.String
	diagnostics.Append(config.GetAttribute(ctx, path.Root("password_wo"), &password)...)
	diagnostics.Append(plan.GetAttribute(ctx, path.Root("password_wo_version"), &version)...)
	if prior != nil {
		diagnostics.Append(prior.GetAttribute(ctx, path.Root("password_wo_version"), &priorVersion)...)
	}
	if diagnostics.HasError() {
		return
	}
	if !password.IsNull() && !password.IsUnknown() && (prior == nil || !version.Equal(priorVersion)) {
		words = append(words, "=password="+password.ValueString())
	}
	if len(words) > 1 {
		if _, _, err := r.client.run(ctx, words...); err != nil {
			diagnostics.AddError("Write container configuration", err.Error())
			return
		}
	}
	row, err := r.read(ctx)
	if err != nil {
		diagnostics.AddError("Verify container configuration", err.Error())
		return
	}
	r.store(ctx, state, plan, row, diagnostics)
}

func (r *containerConfig) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan, req.Config, nil, &resp.State, &resp.Diagnostics)
}

func (r *containerConfig) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.write(ctx, req.Plan, req.Config, &req.State, &resp.State, &resp.Diagnostics)
}

func (r *containerConfig) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !r.ready(&resp.Diagnostics) {
		return
	}
	row, err := r.read(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Read container configuration", err.Error())
		return
	}
	r.store(ctx, &resp.State, req.State, row, &resp.Diagnostics)
}

func (*containerConfig) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning("Container configuration left on the router",
		"routeros_container_config was removed from state only. The router keeps its registry, credentials and directories; change them on the router if needed.")
}

func (*containerConfig) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != containerConfigID {
		resp.Diagnostics.AddError("Invalid import ID", "Import the singleton with the ID "+containerConfigID+".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(containerConfigID))...)
}
