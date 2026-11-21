package integration

import (
	"bufio"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-routeros/routeros/v3/proto"
)

// Run the real Terraform lifecycle against an in-process API-SSL test double.
// The same script also runs against a disposable CHR.
func TestTerraformLifecycle(t *testing.T) {
	terraform := os.Getenv("TERRAFORM_BIN")
	if terraform == "" {
		t.Skip("set TERRAFORM_BIN to enable the wire-level Terraform lifecycle test")
	}
	work := t.TempDir()
	binary := filepath.Join(work, "terraform-provider-routeros")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../cmd/terraform-provider-routeros")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	rc := filepath.Join(work, "terraform.rc")
	config := "provider_installation {\n dev_overrides {\n \"vbargl/routeros\" = \"" + work + "\"\n }\n direct {}\n}\n"
	if err := os.WriteFile(rc, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	source := httptest.NewTLSServer(http.NotFoundHandler())
	defer source.Close()
	ca := filepath.Join(work, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: source.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: source.TLS.Certificates, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		fixture := &wireguardFixture{}
		for {
			conn, err := listener.Accept()
			if errors.Is(err, net.ErrClosed) {
				if fixture.row != nil {
					done <- errors.New("Terraform left the fixture on the router")
					return
				}
				done <- nil
				return
			}
			if err != nil {
				done <- err
				return
			}
			err = fixture.serve(conn)
			_ = conn.Close()
			if err != nil {
				_ = listener.Close()
				done <- err
				return
			}
		}
	}()
	command := exec.CommandContext(t.Context(), "nu", "--no-config-file", "chr.nu")
	command.Env = append(os.Environ(),
		"TERRAFORM_BIN="+terraform, "ROUTEROS_DISPOSABLE_CHR=yes", "TF_CLI_CONFIG_FILE="+rc,
		"TF_VAR_address="+listener.Addr().String(), "TF_VAR_ca_cert_file="+ca,
		"ROUTEROS_USERNAME=fixture", "ROUTEROS_PASSWORD=fixture-password",
	)
	output, runErr := command.CombinedOutput()
	_ = listener.Close()
	if err := <-done; err != nil {
		t.Error(err)
	}
	if runErr != nil {
		t.Fatalf("Terraform lifecycle: %v\n%s", runErr, output)
	}
	if strings.Contains(string(output), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=") || strings.Contains(string(output), "fixture-password") {
		t.Fatal("a key or credential appeared in Terraform logs")
	}
}

type wireguardFixture struct{ row map[string]string }

func sendReply(writer proto.Writer, words ...string) error {
	writer.BeginSentence()
	for _, word := range words {
		writer.WriteWord(word)
	}
	return writer.EndSentence()
}

func (f *wireguardFixture) serve(conn net.Conn) error {
	reader, writer := bufio.NewReader(conn), proto.NewWriter(conn)
	login, err := readFixtureCommand(reader)
	if err != nil {
		return err
	}
	if login.Word != "/login" || login.Map["name"] != "fixture" {
		return errors.New("unexpected API-SSL login")
	}
	if login.Map["password"] != "fixture-password" {
		if err := sendReply(writer, "!trap", "=message="+login.Map["password"]); err != nil {
			return err
		}
		return sendReply(writer, "!done")
	}
	if err := sendReply(writer, "!done"); err != nil {
		return err
	}
	command, err := readFixtureCommand(reader)
	// Terraform closes the authenticated connection if its request is cancelled.
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, field := range []string{"private-key", "preshared-key"} {
		if _, present := command.Map[field]; present {
			return errors.New("ordinary write sent secret key material")
		}
	}
	switch command.Word {
	case "/interface/wireguard/print":
		if command.Map[".proplist"] != ".id,name,listen-port,mtu,comment,disabled,public-key" || command.Map["?.id"] != "*A" {
			return errors.New("ordinary read used an unexpected WireGuard projection")
		}
		if f.row != nil {
			words := []string{"!re", "=.id=*A"}
			for key, value := range f.row {
				words = append(words, "="+key+"="+value)
			}
			if err := sendReply(writer, words...); err != nil {
				return err
			}
		}
	case "/interface/wireguard/add":
		if f.row != nil {
			return errors.New("duplicate WireGuard interface")
		}
		f.row = map[string]string{"public-key": "fixture-public-identity"}
		for key, value := range command.Map {
			f.row[key] = value
		}
		return sendReply(writer, "!done", "=ret=*A")
	case "/interface/wireguard/set":
		if f.row == nil || command.Map[".id"] != "*A" {
			return errors.New("set targeted an unknown interface")
		}
		for key, value := range command.Map {
			if key != ".id" {
				f.row[key] = value
			}
		}
	case "/interface/wireguard/remove":
		if f.row == nil || command.Map[".id"] != "*A" {
			return errors.New("remove targeted an unknown interface")
		}
		f.row = nil
	default:
		return errors.New("unexpected fixture command")
	}
	return sendReply(writer, "!done")
}

// Fixture commands fit the protocol's single-byte word length encoding.
func readFixtureCommand(reader *bufio.Reader) (*proto.Sentence, error) {
	result := proto.NewSentence()
	for {
		size, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return result, nil
		}
		if size > 127 {
			return nil, errors.New("unexpected fixture word length")
		}
		word := make([]byte, size)
		if _, err := io.ReadFull(reader, word); err != nil {
			return nil, err
		}
		if result.Word == "" {
			result.Word = string(word)
			continue
		}
		key, value, _ := strings.Cut(strings.TrimPrefix(string(word), "="), "=")
		result.Map[key] = value
	}
}
