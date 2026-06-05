package network

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/apache/arrow/go/v18/arrow/flight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/galgotech/heddle-lang/pkg/logger"
	"github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime"
	"github.com/galgotech/heddle-lang/pkg/schema"

	"github.com/galgotech/heddle-sdk-go/internal/executor/execute"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
)

type flightNetworkClient struct {
	namespace string
	language  string
	ready     chan struct{}
	registry  registry.Registry
	executor  execute.Executor
}

func NewNetworkClient(
	namespace string,
	language string,
	ready chan struct{},
	reg registry.Registry,
	exec execute.Executor,
) *flightNetworkClient {
	return &flightNetworkClient{
		namespace: namespace,
		language:  language,
		ready:     ready,
		registry:  reg,
		executor:  exec,
	}
}

// Run initializes the plugin's lifecycle, establishing a resilient connection to the Worker.
// It manages registration, heartbeats, and the bidirectional execution stream.
func (nc *flightNetworkClient) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Ensure all active resources are closed when this method returns.
	defer nc.registry.CloseAllResources()

	var (
		opts []grpc.DialOption
		err  error
	)

	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	var (
		conn   *grpc.ClientConn
		client flight.Client
	)

	// 1. Start Retry Loop

	for {
		// 1.1 Connect to Worker (handle UDS if path starts with / or unix:)
		target := runtime.WorkerUDSPath

		// Establish the gRPC connection to the Worker.
		conn, err = grpc.NewClient(target, opts...)
		if err != nil {
			logger.L().Info("Worker not reachable, retrying...", logger.String("target", target))

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
				continue
			}
		}

		client = flight.NewClientFromConn(conn, nil)

		// 1.2 Register Plugin
		allSteps := nc.registry.AllSteps()
		allResources := nc.registry.AllResources()

		logger.L().Info("Preparing plugin registration", logger.Int("steps", len(allSteps)), logger.Int("resources", len(allResources)))

		schemas := make(map[string]schema.StepSchemas)

		for name, step := range allSteps {
			capName := fmt.Sprintf("%s.%s", nc.namespace, name)
			schemas[capName] = schema.StepSchemas{
				Config:        step.ConfigSchema,
				Input:         step.InputSchema,
				Output:        step.OutputSchema,
				Documentation: step.Documentation,
				SourceCode:    step.SourceCode,
				SourceFile:    step.SourceFile,
				SourceLine:    step.SourceLine,
			}
		}

		resources := make(map[string]schema.FieldSchema)
		for name, res := range allResources {
			resources[name] = res.FieldSchema
		}

		registration := plugin.PluginRegistration{
			Namespace: nc.namespace,
			Language:  nc.language,
			Version:   "0.1.0",
			Schemas:   schemas,
			Resources: resources,
		}

		registrationBody, err := json.Marshal(registration)
		if err != nil {
			logger.L().Info("Failed to marshal plugin registration...", logger.String("target", target))
			return err
		}

		// Submit registration via Arrow Flight DoAction.
		// This notifies the Worker of the plugin's namespace and step capabilities.
		res, err := client.DoAction(ctx, &flight.Action{
			Type: plugin.ActionRegisterPlugin,
			Body: registrationBody,
		})
		if err != nil {
			logger.L().Info("Retrying plugin registration...", logger.String("target", target))
			conn.Close()

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
				continue
			}
		}

		// Block until the Worker acknowledges registration.
		if _, err := res.Recv(); err != nil {
			logger.L().Info("Waiting for registration result...", logger.String("target", target))
			conn.Close()

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
				continue
			}
		}

		logger.L().Info("Plugin registered", logger.String("namespace", nc.namespace))
		logger.L().Debug("Plugin registered", logger.String("resources", fmt.Sprintf("%v", registration.Resources)))
		logger.L().Debug("Plugin registered", logger.String("schemas", fmt.Sprintf("%v", registration.Schemas)))

		// 1.3 Start Heartbeat and Execution Loop
		// We use a separate context for each connection session
		sessionCtx, cancel := context.WithCancel(ctx)
		go nc.startHeartbeat(sessionCtx, client)

		if nc.ready != nil {
			// Only close ready once
			select {
			case <-nc.ready:
			default:
				close(nc.ready)
			}
		}

		err = nc.startExecutionLoop(sessionCtx, client)

		cancel() // Stop heartbeat

		conn.Close()

		if err != nil {
			logger.L().Info("Worker connection lost, reconnecting...", logger.String("target", target))

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
				continue
			}
		}

		return nil // Graceful shutdown
	}
}

// startHeartbeat periodically signals the plugin's health and availability to the Worker.
func (nc *flightNetworkClient) startHeartbeat(ctx context.Context, client flight.Client) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hb := plugin.Heartbeat{
				Namespace: nc.namespace,
				Timestamp: time.Now(),
				Status:    "ready",
			}
			body, _ := json.Marshal(hb)

			_, err := client.DoAction(ctx, &flight.Action{
				Type: plugin.ActionPluginHeartbeat,
				Body: body,
			})
			if err != nil {
				logger.L().Error("Heartbeat failed", logger.Error(err))
			}
		case <-ctx.Done():
			return
		}
	}
}

// startExecutionLoop opens a bidirectional Arrow Flight exchange for processing step tasks.
func (nc *flightNetworkClient) startExecutionLoop(ctx context.Context, client flight.Client) error {
	// Add namespace to metadata for identification
	md := metadata.Pairs("x-heddle-plugin-namespace", nc.namespace)
	ctx = metadata.NewOutgoingContext(ctx, md)

	stream, err := client.DoExchange(ctx)
	if err != nil {
		return fmt.Errorf("failed to start exchange: %w", err)
	}

	logger.L().Info("Plugin execution loop started", logger.String("namespace", nc.namespace))

	for {
		data, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("exchange stream closed: %w", err)
		}

		var request plugin.ExecuteStepRequest
		if err := json.Unmarshal(data.DataBody, &request); err != nil {
			logger.L().Error("Failed to unmarshal request", logger.Error(err))
			continue
		}

		// Execute task in a goroutine
		go func(r plugin.ExecuteStepRequest) {
			response, err := nc.executor.Execute(ctx, r)
			if err != nil {
				logger.L().Error("Execution failed", logger.Error(err))
				return
			}

			responseBody, err := json.Marshal(response)
			if err != nil {
				logger.L().Error("Failed to marshal response", logger.Error(err))
				return
			}

			if err := stream.Send(&flight.FlightData{DataBody: responseBody}); err != nil {
				logger.L().Error("Failed to send response", logger.Error(err))
			}
		}(request)
	}
}
