package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type runFunc func(context.Context, ...string) ([]map[string]string, string, error)

func (f runFunc) run(ctx context.Context, command ...string) ([]map[string]string, string, error) {
	return f(ctx, command...)
}

func TestWireGuardReadRequestsOnlyPublicFields(t *testing.T) {
	r := managedObject{
		definition: objects[0],
		client: runFunc(func(_ context.Context, command ...string) ([]map[string]string, string, error) {
			expected := []string{"/interface/wireguard/print", "=.proplist=.id,name,listen-port,mtu,comment,disabled,public-key", "?.id=*A"}
			if !slices.Equal(command, expected) {
				t.Fatalf("unexpected WireGuard read: %v", command)
			}
			return []map[string]string{{".id": "*A", "public-key": "public identity"}}, "", nil
		}),
	}
	row, err := r.read(t.Context(), "*A")
	if err != nil || row["public-key"] != "public identity" {
		t.Fatalf("read failed: %v", err)
	}
}

func TestReadDoesNotConfuseTransportErrorWithDeletion(t *testing.T) {
	denied := errors.New("permission denied")
	r := managedObject{definition: objects[0], client: runFunc(func(context.Context, ...string) ([]map[string]string, string, error) {
		return nil, "", denied
	})}
	if _, err := r.read(t.Context(), "*A"); !errors.Is(err, denied) {
		t.Fatalf("expected permission failure, got %v", err)
	}
}

func TestImportRejectsDynamicLeases(t *testing.T) {
	r := managedObject{
		definition: objects[7],
		client: runFunc(func(_ context.Context, args ...string) ([]map[string]string, string, error) {
			if !strings.Contains(args[1], ",dynamic") {
				t.Fatal("dynamic status omitted from projection")
			}
			return []map[string]string{{".id": "*A", "dynamic": "true"}}, "", nil
		}),
	}
	if _, err := r.read(t.Context(), "*A"); err == nil {
		t.Fatal("expected dynamic lease rejection")
	}
}
