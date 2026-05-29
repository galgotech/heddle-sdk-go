package network

import (
	"context"
	"fmt"

	"github.com/galgotech/heddle-sdk-go/internal/executor/execute"
	"github.com/galgotech/heddle-sdk-go/internal/executor/history"
	"github.com/galgotech/heddle-sdk-go/internal/network"
	"github.com/galgotech/heddle-sdk-go/plugin"
)

// Run initiates the worker's connection for one or more plugins concurrently.
func Run(ctx context.Context, plugins ...*plugin.Plugin) error {
	if len(plugins) == 0 {
		return fmt.Errorf("no plugins provided")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errChan := make(chan error, len(plugins))
	for _, p := range plugins {
		go func(p *plugin.Plugin) {
			workerHistory := history.NewWorkerHistory()
			executor := execute.NewWorkerExecutor(p.Registry(), workerHistory)

			client := network.NewNetworkClient(
				p.GetNamespace(),
				plugin.Language,
				p.GetReady(),
				p.Registry(),
				executor,
			)

			err := client.Run(ctx)
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
