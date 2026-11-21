package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	ros "github.com/go-routeros/routeros/v3"
)

type commandRunner interface {
	run(context.Context, ...string) ([]map[string]string, string, error)
}

type routerClient struct {
	address            string
	tlsConfig          *tls.Config
	username, password string
	orderedLocks       map[string]*sync.Mutex
}

func (c *routerClient) run(ctx context.Context, words ...string) ([]map[string]string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	dialer := tls.Dialer{Config: c.tlsConfig}
	conn, err := dialer.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return nil, "", errors.New("API-SSL connection or certificate verification failed")
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, "", errors.New("could not set API-SSL deadline")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	client, err := ros.NewClient(conn)
	if err != nil {
		return nil, "", errors.New("could not initialize API-SSL client")
	}
	defer func() { _ = client.Close() }()
	client.SetLogHandler(slog.NewTextHandler(io.Discard, nil))
	if err := client.LoginContext(ctx, c.username, c.password); err != nil {
		return nil, "", errors.New("API-SSL authentication failed")
	}
	reply, err := client.RunArgsContext(ctx, words)
	if err != nil {
		// RouterOS diagnostics can contain command arguments or response values.
		return nil, "", errors.New("RouterOS rejected the command or the connection was interrupted")
	}
	rows := make([]map[string]string, 0, len(reply.Re))
	for _, row := range reply.Re {
		rows = append(rows, row.Map)
	}
	return rows, reply.Done.Map["ret"], nil
}
