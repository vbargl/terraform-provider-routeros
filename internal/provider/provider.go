package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type routerProvider struct{ version string }

// New returns the constructor used by the Terraform plugin server.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &routerProvider{version: version} }
}

type providerConfig struct {
	Address         types.String `tfsdk:"address"`
	CACertFile      types.String `tfsdk:"ca_cert_file"`
	EphemeralAccess types.Bool   `tfsdk:"ephemeral_access"`
}

func (p *routerProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName, resp.Version = "routeros", p.version
}

func (*routerProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"address":          schema.StringAttribute{Required: true, Description: "API-SSL host and port."},
		"ca_cert_file":     schema.StringAttribute{Optional: true, Description: "Additional trusted CA PEM file."},
		"ephemeral_access": schema.BoolAttribute{Optional: true, Description: "Select the dedicated key-reading credentials and disable managed resources for this configuration."},
	}}
}

func (*routerProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerConfig
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Address.IsUnknown() || cfg.CACertFile.IsUnknown() || cfg.EphemeralAccess.IsUnknown() {
		resp.Diagnostics.AddError("Unknown RouterOS configuration", "API-SSL settings must be known before the provider is configured.")
		return
	}
	address := cfg.Address.ValueString()
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		resp.Diagnostics.AddError("Invalid RouterOS address", "Use an API-SSL host and port, for example router.example:8729.")
		return
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if path := cfg.CACertFile.ValueString(); path != "" {
		contents, err := os.ReadFile(path)
		roots, rootsErr := x509.SystemCertPool()
		if err != nil || rootsErr != nil || !roots.AppendCertsFromPEM(contents) {
			resp.Diagnostics.AddError("Invalid RouterOS CA file", "Supply a readable PEM file containing a CA certificate.")
			return
		}
		tlsConfig.RootCAs = roots
	}
	prefix := "ROUTEROS_"
	if cfg.EphemeralAccess.ValueBool() {
		prefix = "ROUTEROS_EPHEMERAL_"
	}
	client := &routerClient{address: address, tlsConfig: tlsConfig, username: os.Getenv(prefix + "USERNAME"), password: os.Getenv(prefix + "PASSWORD")}
	if client.username == "" || client.password == "" {
		resp.Diagnostics.AddError("Missing RouterOS credentials", "Set "+prefix+"USERNAME and "+prefix+"PASSWORD in the provider process environment.")
		return
	}
	if cfg.EphemeralAccess.ValueBool() {
		resp.EphemeralResourceData = client
	} else {
		resp.ResourceData, resp.DataSourceData = client, client
	}
}

func (*routerProvider) Resources(context.Context) []func() resource.Resource {
	result := make([]func() resource.Resource, 0, len(objects))
	for _, object := range objects {
		result = append(result, func() resource.Resource { return &managedObject{definition: object} })
	}
	return append(result,
		func() resource.Resource { return &container{} },
		func() resource.Resource { return &containerConfig{} },
	)
}
func (*routerProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		func() datasource.DataSource { return &dynamicLeases{} },
		func() datasource.DataSource { return &deviceMode{} },
		func() datasource.DataSource { return &systemPackage{} },
	}
}
func (*routerProvider) EphemeralResources(context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{func() ephemeral.EphemeralResource { return &wireguardKey{} }}
}
