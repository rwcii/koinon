package main

import (
	"context"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/install"
)

// installRemoteEnable enables remote access for the install answer "this computer and your
// tailnet" (#665), as koinon remote enable --https-port does, with the operator question on
// the install's own terminal.
func installRemoteEnable(t *terminal) func(ctx context.Context, state, address string, port int) (string, error) {
	return func(ctx context.Context, state, address string, port int) (string, error) {
		bearer, err := core.ClientSecret(state, address)
		if err != nil {
			return "", err
		}
		call := func() error {
			_, err := core.Call(ctx, address, bearer, "/v1/remote/enable", map[string]int{"https_port": port})
			return err
		}
		if err := call(); err != nil {
			if err = grantOperator(ctx, t, nil, err, call); err != nil {
				return "", err
			}
		}
		return install.RemoteURL(ctx, address, bearer)
	}
}
