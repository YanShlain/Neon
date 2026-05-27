package temporal

import (
	"context"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

func startDevServer(ctx context.Context, namespace string) (client.Client, func() error, error) {
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		ClientOptions: &client.Options{Namespace: namespace},
	})
	if err != nil {
		return nil, nil, err
	}
	return srv.Client(), srv.Stop, nil
}
