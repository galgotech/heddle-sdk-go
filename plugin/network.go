package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/apache/arrow/go/v18/arrow/flight"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	heddleruntime "github.com/galgotech/heddle-lang/pkg/runtime"
	"github.com/galgotech/heddle-lang/pkg/schema"
)

type flightNetworkClient struct {
	namespace       string
	language        string
	ready           chan struct{}
	registry        Registry
	executor        Executor
	resourceManager *ResourceManager
}

func NewNetworkClient(
	namespace string,
	language string,
	ready chan struct{},
	reg Registry,
	exec Executor,
	rm *ResourceManager,
) NetworkClient {
	return &flightNetworkClient{
		namespace:       namespace,
		language:        language,
		ready:           ready,
		registry:        reg,
		executor:        exec,
		resourceManager: rm,
	}
}

// Start initializes the plugin's lifecycle, establishing a resilient connection to the Worker.
// It manages registration, heartbeats, and the bidirectional execution stream.
func (nc *flightNetworkClient) Start(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Ensure all active resources are closed and cleaner stopped when this method returns.
	defer nc.resourceManager.CloseAll()

	// Start resource cleaner using the signal context so that it terminates automatically.
	nc.resourceManager.StartCleaner(ctx)

	var opts []grpc.DialOption
	var err error
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	var conn *grpc.ClientConn
	var client flight.Client

	// 1. Start Retry Loop
	for {
		// 1.1 Connect to Worker (handle UDS if path starts with / or unix:)
		target := heddleruntime.WorkerUDSPath
		// Establish the gRPC connection to the Worker.
		conn, err = grpc.NewClient(target, opts...)
		if err != nil {
			logger.L().Info("Worker not reachable, retrying...", zap.String("target", target))

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

		logger.L().Info("Preparing plugin registration", zap.Int("steps", len(allSteps)), zap.Int("resources", len(allResources)))
		capabilities := make([]string, 0, len(allSteps)+len(allResources))
		schemas := make(map[string]schema.StepSchemas)
		for name, step := range allSteps {
			capName := fmt.Sprintf("%s.%s", nc.namespace, name)
			capabilities = append(capabilities, capName)
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

		for name := range allResources {
			capabilities = append(capabilities, fmt.Sprintf("%s.resource.%s", nc.namespace, name))
		}

		resources := make(map[string]*schema.ResourceAndConfigSchema)
		for name, res := range allResources {
			resources[name] = res.ResourceSchema
		}

		reg := baseplugin.PluginRegistration{
			Namespace:    nc.namespace,
			Language:     nc.language,
			Version:      "0.1.0",
			Capabilities: capabilities,
			Schemas:      schemas,
			Resources:    resources,
		}
		regBody, err := json.Marshal(reg)
		if err != nil {
			logger.L().Info("Failed to marshal plugin registration...", zap.String("target", target))
			return err
		}

		// Submit registration via Arrow Flight DoAction.
		// This notifies the Worker of the plugin's namespace and step capabilities.
		res, err := client.DoAction(ctx, &flight.Action{
			Type: baseplugin.ActionRegisterPlugin,
			Body: regBody,
		})
		if err != nil {
			logger.L().Info("Retrying plugin registration...", zap.String("target", target))
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
			logger.L().Info("Waiting for registration result...", zap.String("target", target))
			conn.Close()

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
				continue
			}
		}

		logger.L().Info("Plugin registered", zap.String("namespace", nc.namespace))
		logger.L().Debug("Plugin registered", zap.String("capabilities", fmt.Sprintf("%v", reg.Capabilities)))
		logger.L().Debug("Plugin registered", zap.String("resources", fmt.Sprintf("%v", reg.Resources)))
		logger.L().Debug("Plugin registered", zap.String("schemas", fmt.Sprintf("%v", reg.Schemas)))

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
			logger.L().Info("Worker connection lost, reconnecting...", zap.String("target", target))
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
			hb := baseplugin.Heartbeat{
				Namespace: nc.namespace,
				Timestamp: time.Now(),
				Status:    "ready",
			}
			body, _ := json.Marshal(hb)
			_, err := client.DoAction(ctx, &flight.Action{
				Type: baseplugin.ActionPluginHeartbeat,
				Body: body,
			})
			if err != nil {
				logger.L().Error("Heartbeat failed", zap.Error(err))
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

	logger.L().Info("Plugin execution loop started", zap.String("namespace", nc.namespace))

	for {
		data, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("exchange stream closed: %w", err)
		}

		var req baseplugin.ExecuteStepRequest
		if err := json.Unmarshal(data.DataBody, &req); err != nil {
			logger.L().Error("Failed to unmarshal request", zap.Error(err))
			continue
		}

		// Execute task in a goroutine
		go func(r baseplugin.ExecuteStepRequest) {
			resp := nc.executor.ExecuteTask(ctx, r)
			respBody, err := json.Marshal(resp)
			if err != nil {
				logger.L().Error("Failed to unmarshal response", zap.Error(err))
				return
			}
			if err := stream.Send(&flight.FlightData{DataBody: respBody}); err != nil {
				logger.L().Error("Failed to send response", zap.Error(err))
			}
		}(req)
	}
}
