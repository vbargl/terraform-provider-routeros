package integration

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-routeros/routeros/v3/proto"
)

// Run real Terraform against an API-SSL test double of /container/envs and
// /container/mounts: create lists with several entries, detect an entry added on
// the router, converge, change entries and destroy.
func TestContainerListsOverAPI(t *testing.T) {
	terraform := os.Getenv("TERRAFORM_BIN")
	if terraform == "" {
		t.Skip("set TERRAFORM_BIN to enable the wire-level Terraform test")
	}
	work := t.TempDir()
	binary := filepath.Join(work, "plugins", "terraform-provider-routeros")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../cmd/terraform-provider-routeros")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	rc := filepath.Join(work, "terraform.rc")
	config := "provider_installation {\n dev_overrides {\n \"vbargl/routeros\" = \"" + filepath.Dir(binary) + "\"\n }\n direct {}\n}\n"
	if err := os.WriteFile(rc, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.tf", "versions.tf"} {
		contents, err := os.ReadFile(filepath.Join("lists", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
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
	fixture := &listFixture{rows: map[string][]map[string]string{}, next: 0x10}
	fixture.add("/container/envs", map[string]string{"list": "other", "key": "KEEP", "value": "me"})
	done := make(chan error, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if errors.Is(err, net.ErrClosed) {
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
				done <- err
				_ = listener.Close()
				return
			}
		}
	}()
	defer func() {
		_ = listener.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	run := func(args ...string) (string, int) {
		t.Helper()
		command := exec.CommandContext(t.Context(), terraform, append([]string{"-chdir=" + work}, args...)...)
		command.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1",
			"TF_VAR_address="+listener.Addr().String(), "TF_VAR_ca_cert_file="+ca,
			"ROUTEROS_USERNAME=fixture", "ROUTEROS_PASSWORD=fixture-password")
		output, err := command.CombinedOutput()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(output), code
	}
	vars := func(env map[string]string, mount map[string][2]string) []string {
		mounts := map[string]map[string]string{}
		for dst, entry := range mount {
			mounts[dst] = map[string]string{"src": entry[0], "mode": entry[1]}
		}
		encodedEnv, _ := json.Marshal(env)
		encodedMount, _ := json.Marshal(mounts)
		return []string{"-var", "env=" + string(encodedEnv), "-var", "mount=" + string(encodedMount)}
	}
	env := map[string]string{"KNOCKERUP_NAMESPACE": "knockerup", "POOL": "suspendable", "HOME": "/"}
	mount := map[string][2]string{"/etc/knockerup/kubeconfig": {"/knockerup/kubeconfig", "ro"}, "/etc/knockerup/ca": {"/knockerup/ca", "ro"}}

	if output, code := run(append([]string{"apply", "-auto-approve", "-no-color", "-input=false"}, vars(env, mount)...)...); code != 0 {
		t.Fatalf("apply: %s", output)
	}
	if got := fixture.keys("/container/envs", "agent", "key"); !slices.Equal(got, []string{"HOME", "KNOCKERUP_NAMESPACE", "POOL"}) {
		t.Fatalf("env entries = %v", got)
	}
	if got := fixture.keys("/container/mounts", "agent", "dst"); len(got) != 2 {
		t.Fatalf("mount entries = %v", got)
	}
	ids := fixture.ids("/container/envs", "agent", "key")

	// An entry added outside Terraform shows as drift and is removed on apply.
	fixture.add("/container/envs", map[string]string{"list": "agent", "key": "EXTRA", "value": "x"})
	output, code := run(append([]string{"plan", "-detailed-exitcode", "-no-color", "-input=false"}, vars(env, mount)...)...)
	if code != 2 || !strings.Contains(output, "EXTRA") || !strings.Contains(output, "update in-place") {
		t.Fatalf("drift not reported (exit %d): %s", code, output)
	}
	if output, code := run(append([]string{"apply", "-auto-approve", "-no-color", "-input=false"}, vars(env, mount)...)...); code != 0 {
		t.Fatalf("converge: %s", output)
	}
	if got := fixture.keys("/container/envs", "agent", "key"); slices.Contains(got, "EXTRA") || len(got) != 3 {
		t.Fatalf("unmanaged entry kept: %v", got)
	}

	// Remove one entry and add another; unchanged entries keep their IDs.
	delete(env, "HOME")
	env["TZ"] = "UTC"
	if output, code := run(append([]string{"apply", "-auto-approve", "-no-color", "-input=false"}, vars(env, mount)...)...); code != 0 {
		t.Fatalf("change: %s", output)
	}
	after := fixture.ids("/container/envs", "agent", "key")
	if after["POOL"] != ids["POOL"] || after["KNOCKERUP_NAMESPACE"] != ids["KNOCKERUP_NAMESPACE"] || after["HOME"] != "" || after["TZ"] == "" {
		t.Fatalf("entries were not converged individually: before %v, after %v", ids, after)
	}
	if output, code := run(append([]string{"plan", "-detailed-exitcode", "-no-color", "-input=false"}, vars(env, mount)...)...); code != 0 {
		t.Fatalf("plan after apply is not empty (exit %d): %s", code, output)
	}

	if output, code := run(append([]string{"destroy", "-auto-approve", "-no-color", "-input=false"}, vars(env, mount)...)...); code != 0 {
		t.Fatalf("destroy: %s", output)
	}
	if len(fixture.keys("/container/envs", "agent", "key")) != 0 || len(fixture.keys("/container/mounts", "agent", "dst")) != 0 {
		t.Fatal("destroy left entries behind")
	}
	if !slices.Equal(fixture.keys("/container/envs", "other", "key"), []string{"KEEP"}) {
		t.Fatal("another list was modified")
	}
}

type listFixture struct {
	mu   sync.Mutex
	rows map[string][]map[string]string
	next int
}

func (f *listFixture) add(menu string, row map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	row[".id"] = fmt.Sprintf("*%X", f.next)
	f.next++
	row["disabled"] = "false"
	f.rows[menu] = append(f.rows[menu], row)
	return row[".id"]
}

func (f *listFixture) ids(menu, list, key string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := map[string]string{}
	for _, row := range f.rows[menu] {
		if row["list"] == list {
			result[row[key]] = row[".id"]
		}
	}
	return result
}

func (f *listFixture) keys(menu, list, key string) []string {
	var result []string
	for name := range f.ids(menu, list, key) {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

func (f *listFixture) serve(conn net.Conn) error {
	reader, writer := bufio.NewReader(conn), proto.NewWriter(conn)
	login, err := readFixtureCommand(reader)
	if err != nil {
		return err
	}
	if login.Word != "/login" || login.Map["name"] != "fixture" || login.Map["password"] != "fixture-password" {
		return errors.New("unexpected API-SSL login")
	}
	if err := sendReply(writer, "!done"); err != nil {
		return err
	}
	command, err := readFixtureCommand(reader)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	slash := strings.LastIndex(command.Word, "/")
	menu, verb := command.Word[:slash], command.Word[slash+1:]
	fields := map[string][]string{"/container/envs": {".id", "list", "key", "value", "disabled"}, "/container/mounts": {".id", "list", "dst", "src", "mode", "disabled"}}[menu]
	if fields == nil {
		return fmt.Errorf("unexpected menu %s", command.Word)
	}
	switch verb {
	case "print":
		if command.Map[".proplist"] != strings.Join(fields, ",") || command.Map["?list"] == "" {
			return fmt.Errorf("unexpected list projection %v", command.Map)
		}
		f.mu.Lock()
		var replies [][]string
		for _, row := range f.rows[menu] {
			if row["list"] != command.Map["?list"] {
				continue
			}
			words := []string{"!re"}
			for _, field := range fields {
				words = append(words, "="+field+"="+row[field])
			}
			replies = append(replies, words)
		}
		f.mu.Unlock()
		for _, words := range replies {
			if err := sendReply(writer, words...); err != nil {
				return err
			}
		}
	case "add":
		row := map[string]string{}
		for key, value := range command.Map {
			row[key] = value
		}
		return sendReply(writer, "!done", "=ret="+f.add(menu, row))
	case "set", "remove":
		f.mu.Lock()
		found := false
		for index, row := range f.rows[menu] {
			if row[".id"] != command.Map[".id"] {
				continue
			}
			found = true
			if verb == "remove" {
				f.rows[menu] = slices.Delete(f.rows[menu], index, index+1)
				break
			}
			for key, value := range command.Map {
				row[key] = value
			}
		}
		f.mu.Unlock()
		if !found {
			return fmt.Errorf("%s targeted an unknown entry", command.Word)
		}
	default:
		return fmt.Errorf("unexpected command %s", command.Word)
	}
	return sendReply(writer, "!done")
}
