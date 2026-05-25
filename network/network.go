package network

import (
	"context"
	"fmt"

	"github.com/galgotech/heddle-sdk-go/internal/executor/execute"
	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	internalnetwork "github.com/galgotech/heddle-sdk-go/internal/network"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

// Plugin represents the minimal interface required by the network client.
type Plugin interface {
	GetNamespace() string
	GetReady() chan struct{}
	Registry() registry.Registry
}

// Run initiates the worker's connection for one or more plugins concurrently.
func Run(ctx context.Context, plugins ...Plugin) error {
	if len(plugins) == 0 {
		return fmt.Errorf("no plugins provided")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errChan := make(chan error, len(plugins))

	for _, p := range plugins {
		go func(p Plugin) {
			workerHistory := history.NewWorkerHistory()
			exec := execute.NewWorkerExecutor(p.Registry(), workerHistory)

			client := internalnetwork.NewNetworkClient(
				p.GetNamespace(),
				"go",
				p.GetReady(),
				p.Registry(),
				exec,
			)

			err := client.Start(ctx)
			if err != nil {
				errChan <- fmt.Errorf("plugin %s failed: %w", p.GetNamespace(), err)
			} else {
				errChan <- nil
			}
		}(p)
	}

	var firstErr error
	for range plugins {
		select {
		case err := <-errChan:
			if err != nil && firstErr == nil {
				firstErr = err
				cancel()
			}
		case <-ctx.Done():
			if firstErr == nil {
				firstErr = ctx.Err()
			}
		}
	}

	return firstErr
}
