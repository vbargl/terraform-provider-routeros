package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-routeros/routeros/v3/proto"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func TestEphemeralKeyUsesDedicatedProjection(t *testing.T) {
	resource := wireguardKey{}
	var schema ephemeral.SchemaResponse
	resource.Schema(t.Context(), ephemeral.SchemaRequest{}, &schema)
	config := tfsdk.State{Schema: schema.Schema}
	values := keyValues{InterfaceID: types.StringValue("*A"), PrivateKey: types.StringNull()}
	if diagnostics := config.Set(t.Context(), values); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	request := ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: config.Raw}}
	response := ephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Schema: schema.Schema}}
	resource.Open(t.Context(), request, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("key read allowed without dedicated configuration")
	}

	resource.client = runFunc(func(_ context.Context, words ...string) ([]map[string]string, string, error) {
		if !slices.Equal(words, []string{"/interface/wireguard/print", "=.proplist=.id,private-key", "?.id=*A"}) {
			t.Fatalf("unexpected key read projection: %v", words)
		}
		return []map[string]string{{".id": "*A", "private-key": testKey}}, "", nil
	})
	response.Diagnostics = nil
	resource.Open(t.Context(), request, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if diagnostics := response.Result.Get(t.Context(), &values); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if values.PrivateKey.ValueString() != testKey {
		t.Fatal("ephemeral response did not contain the requested value")
	}
}

func TestTLSAuthenticationAndRedactedTrap(t *testing.T) {
	certificateSource := httptest.NewTLSServer(http.NotFoundHandler())
	defer certificateSource.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: certificateSource.TLS.Certificates,
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	serverDone := make(chan error, 1)
	go func() {
		// The untrusted connection fails before the RouterOS login arrives.
		for attempt := 0; attempt < 2; attempt++ {
			conn, err := listener.Accept()
			if err != nil {
				serverDone <- err
				return
			}
			err = serveTestConnection(conn)
			_ = conn.Close()
			if attempt == 1 {
				serverDone <- err
			}
		}
	}()
	address := listener.Addr().String()
	client := routerClient{address: address, username: "test", password: "fixture-password", tlsConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if _, _, err := client.run(t.Context(), "/interface/wireguard/print"); err == nil || err.Error() != "API-SSL connection or certificate verification failed" {
		t.Fatalf("expected certificate rejection, got %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificateSource.Certificate())
	client.tlsConfig.RootCAs = roots
	if _, _, err := client.run(t.Context(), "/interface/wireguard/print", "=.proplist=.id,public-key"); err == nil ||
		err.Error() != "RouterOS rejected the command or the connection was interrupted" {
		t.Fatalf("expected sanitized router trap, got %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func serveTestConnection(conn net.Conn) error {
	reader, writer := proto.NewReader(conn), proto.NewWriter(conn)
	login, err := reader.ReadSentence()
	if err != nil {
		return err
	}
	if login.Word != "/login" || login.Map["name"] != "test" || login.Map["password"] != "fixture-password" {
		return errors.New("login did not use the dedicated TLS connection")
	}
	writer.BeginSentence()
	writer.WriteWord("!done")
	if err := writer.EndSentence(); err != nil {
		return err
	}
	print, err := reader.ReadSentence()
	if err != nil {
		return err
	}
	if print.Word != "/interface/wireguard/print" || print.Map[".proplist"] != ".id,public-key" {
		return errors.New("unexpected WireGuard request projection")
	}
	writer.BeginSentence()
	writer.WriteWord("!trap")
	writer.WriteWord("=message=" + testKey)
	if err := writer.EndSentence(); err != nil {
		return err
	}
	writer.BeginSentence()
	writer.WriteWord("!done")
	return writer.EndSentence()
}
