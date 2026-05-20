package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/flight"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	heddleruntime "github.com/galgotech/heddle-lang/pkg/runtime"
	"github.com/galgotech/heddle-lang/pkg/runtime/locality"
	"github.com/galgotech/heddle-lang/pkg/schema"
)

type Plugin struct {
	Namespace string
	Language  string
	resources map[string]resourceRegistration
	steps     map[string]StepRegistration
	Ready     chan struct{}

	activeResources   map[string]Resource
	activeResourcesMu sync.RWMutex
}

// RegisterResource adds a new resource type to the plugin's internal registry.
// These resources can be referenced in .he files to manage external state or connections.
func (p *Plugin) RegisterResource(name string, resource any) error {
	typ := reflect.TypeOf(resource)

	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("resource %q must be a struct or a pointer to a struct", name)
	}

	// Validate that the pointer to this struct implements the Resource interface
	ptrTyp := reflect.PointerTo(typ)
	if !ptrTyp.Implements(reflect.TypeFor[Resource]()) {
		return fmt.Errorf("resource %q (pointer type %s) must implement the Resource interface", name, ptrTyp)
	}

	resourceSchema, err := extractResourceAndConfigSchema(typ)
	if err != nil {
		logger.L().Error("Failed to extract resource schema", zap.String("resource", name), zap.Error(err))
		return fmt.Errorf("resource %q config: %w", name, err)
	}

	// Use reflection on reflect.New(typ) to find the Start method pointer for metadata extraction
	var fnPtr uintptr
	dummyVal := reflect.New(typ)
	if v := dummyVal.MethodByName("Start"); v.IsValid() {
		fnPtr = v.Pointer()
	}
	doc, code, file, line := extractMetadata(fnPtr)

	p.resources[name] = resourceRegistration{
		Name:           name,
		ResourceSchema: resourceSchema,
		ResourceType:   typ,
		Documentation:  doc,
		SourceCode:     code,
		SourceFile:     file,
		SourceLine:     line,
	}

	return nil
}

// RegisterStep registers a Go function as a Heddle Step.
// It performs reflection-based validation of the function signature: func(ctx, config, input) (output, error).
// It also extracts Heddle-compatible schemas from the input and output types for compile-time DSL validation.
func (p *Plugin) RegisterStep(name string, fn any) error {
	typ := reflect.TypeOf(fn)

	if typ.Kind() != reflect.Func {
		return fmt.Errorf("step %q must be a function", name)
	}

	// Ensure the function signature matches one of the expected contracts:
	// func(context.Context, TConfig, TInput, TOutput) error
	if typ.NumIn() != 4 || typ.NumOut() != 1 {
		return fmt.Errorf("step %q must have signature func(ctx, config, input, output) error", name)
	}

	configType := typ.In(1)
	inputType := typ.In(2)
	outputType := typ.In(3)

	configSchema, err := extractResourceAndConfigSchema(configType)
	if err != nil {
		logger.L().Error("Failed to extract step config schema", zap.String("step", name), zap.Error(err))
		return fmt.Errorf("step %q config: %w", name, err)
	}

	inputSchema, err := extractInputOutputSchema(inputType)
	if err != nil {
		logger.L().Error("Failed to extract step input schema", zap.String("step", name), zap.Error(err))
		return fmt.Errorf("step %q input: %w", name, err)
	}

	outputSchema, err := extractInputOutputSchema(outputType)
	if err != nil {
		logger.L().Error("Failed to extract step output schema", zap.String("step", name), zap.Error(err))
		return fmt.Errorf("step %q output: %w", name, err)
	}

	var inputHeddleFrameIndex int
	inputFieldsIndex := []int{}
	inType := inputType.Elem()
	for i := 0; i < inType.NumField(); i++ {
		f := inType.Field(i)
		if f.Type == reflect.TypeFor[HeddleFrame]() || f.Type == reflect.TypeFor[DynamicFrame]() {
			inputHeddleFrameIndex = i
		} else if !f.Anonymous {
			inputFieldsIndex = append(inputFieldsIndex, i)
		}
	}

	var outputHeddleFrameIndex int
	outputFieldsIndex := []int{}
	outType := outputType.Elem()
	for i := 0; i < outType.NumField(); i++ {
		f := outType.Field(i)
		if f.Type == reflect.TypeFor[HeddleFrame]() || f.Type == reflect.TypeFor[DynamicFrame]() {
			outputHeddleFrameIndex = i
		} else if !f.Anonymous {
			outputFieldsIndex = append(outputFieldsIndex, i)
		}
	}

	doc, code, file, line := extractMetadata(reflect.ValueOf(fn).Pointer())

	logger.L().Debug("Registering step", zap.String("name", name))
	p.steps[name] = StepRegistration{
		Name:                   name,
		Func:                   reflect.ValueOf(fn),
		ConfigSchema:           configSchema,
		ConfigType:             configType,
		InputSchema:            inputSchema,
		InputType:              inputType,
		OutputSchema:           outputSchema,
		OutputType:             outputType,
		Documentation:          doc,
		SourceCode:             code,
		SourceFile:             file,
		SourceLine:             line,
		inputHeddleFrameIndex:  inputHeddleFrameIndex,
		outputHeddleFrameIndex: outputHeddleFrameIndex,
		inputFieldsIndex:       inputFieldsIndex,
		outputFieldsIndex:      outputFieldsIndex,
	}

	return nil
}

// Start initializes the plugin's lifecycle, establishing a resilient connection to the Worker.
// It manages registration, heartbeats, and the bidirectional execution stream.
func (p *Plugin) Start() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		logger.L().Info("Preparing plugin registration", zap.Int("steps", len(p.steps)), zap.Int("resources", len(p.resources)))
		capabilities := make([]string, 0, len(p.steps)+len(p.resources))
		schemas := make(map[string]schema.StepSchemas)
		for name, step := range p.steps {
			capName := fmt.Sprintf("%s.%s", p.Namespace, name)
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

		for name := range p.resources {
			capabilities = append(capabilities, fmt.Sprintf("%s.resource.%s", p.Namespace, name))
		}

		resources := make(map[string]*schema.ResourceAndConfigSchema)
		for name, res := range p.resources {
			resources[name] = res.ResourceSchema
		}

		reg := baseplugin.PluginRegistration{
			Namespace:    p.Namespace,
			Language:     p.Language,
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

		logger.L().Info("Plugin registered", zap.String("namespace", p.Namespace))
		logger.L().Debug("Plugin registered", zap.String("capabilities", fmt.Sprintf("%v", reg.Capabilities)))
		logger.L().Debug("Plugin registered", zap.String("resources", fmt.Sprintf("%v", reg.Resources)))
		logger.L().Debug("Plugin registered", zap.String("schemas", fmt.Sprintf("%v", reg.Schemas)))

		// 1.3 Start Heartbeat and Execution Loop
		// We use a separate context for each connection session
		sessionCtx, cancel := context.WithCancel(ctx)

		go p.startHeartbeat(sessionCtx, client)

		if p.Ready != nil {
			// Only close Ready once
			select {
			case <-p.Ready:
			default:
				close(p.Ready)
			}
		}

		err = p.startExecutionLoop(sessionCtx, client)
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
func (p *Plugin) startHeartbeat(ctx context.Context, client flight.Client) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hb := baseplugin.Heartbeat{
				Namespace: p.Namespace,
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
func (p *Plugin) startExecutionLoop(ctx context.Context, client flight.Client) error {
	// Add namespace to metadata for identification
	md := metadata.Pairs("x-heddle-plugin-namespace", p.Namespace)
	ctx = metadata.NewOutgoingContext(ctx, md)

	stream, err := client.DoExchange(ctx)
	if err != nil {
		return fmt.Errorf("failed to start exchange: %w", err)
	}

	logger.L().Info("Plugin execution loop started", zap.String("namespace", p.Namespace))

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
			resp := p.executeTask(ctx, r)
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

// executeTask handles the end-to-end execution of a single Heddle Step.
// It performs Zero-Copy data loading from SHM, reflection-based binding to Go structs,
// function invocation, and result serialization back to SHM.
func (p *Plugin) executeTask(ctx context.Context, request baseplugin.ExecuteStepRequest) baseplugin.ExecuteStepResponse {
	// 1. Resolve the requested step in this plugin's namespace.
	targetStep, ok := p.steps[request.StepName]
	if !ok {
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: fmt.Sprintf("step %s not found", request.StepName),
		}
	}

	// 2. Hydrate the step configuration from the provided JSON.
	configType := targetStep.ConfigType
	if configType.Kind() == reflect.Pointer {
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: "step config must be a struct",
		}
	}

	configVal := reflect.New(configType)
	if request.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(request.ConfigJSON), configVal.Interface()); err != nil {
			return baseplugin.ExecuteStepResponse{
				TaskID:       request.TaskID,
				Status:       baseplugin.StepResponseError,
				ErrorMessage: fmt.Errorf("failed to unmarshal config: %w", err).Error(),
			}
		}
	}

	// 2.1 Auto-initialize and inject resource if provided
	if len(request.Resources) > 0 {
		for rID, resDef := range request.Resources {
			p.activeResourcesMu.RLock()
			_, initialized := p.activeResources[rID]
			p.activeResourcesMu.RUnlock()
			if !initialized {
				logger.L().Info("Auto-initializing resource requested by worker", zap.String("id", rID), zap.String("type", resDef.Type))
				err := p.InitializeResource(ctx, rID, resDef.Type, resDef.Config)
				if err != nil {
					return baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("failed to auto-initialize resource %s: %v", rID, err),
					}
				}
			}
		}
	}

	if request.ResourceId != "" {
		p.activeResourcesMu.RLock()
		res, ok := p.activeResources[request.ResourceId]
		p.activeResourcesMu.RUnlock()
		if ok {
			v := configVal.Elem()
			for _, field := range v.Fields() {
				if field.Type() == reflect.TypeFor[Config]() {
					field.Addr().Interface().(*Config).SetResource(res)
					break
				}
			}
		} else {
			return baseplugin.ExecuteStepResponse{
				TaskID:       request.TaskID,
				Status:       baseplugin.StepResponseError,
				ErrorMessage: fmt.Sprintf("resource %s was not found or initialized", request.ResourceId),
			}
		}
	}

	// 3. Prepare the Input Frame using Zero-Copy SHM access.
	columns := make(map[string]arrow.Array)
	for fieldName, path := range request.InputHandles {
		arr, err := locality.ReadArrowArrayFromPath(path)
		if err != nil {
			logger.L().Error("Failed to read input from SHM", zap.Error(err), zap.String("path", path))
		} else {
			columns[fieldName] = arr
			defer arr.Release()
		}
	}

	inputVal, outputVal := targetStep.newInputOutput()
	if len(columns) > 0 {
		if targetStep.InputType == reflect.TypeFor[*DynamicFrame]() {
			var df *DynamicFrame
			if targetStep.InputType == reflect.TypeFor[*DynamicFrame]() {
				df = inputVal.Interface().(*DynamicFrame)
			} else {
				v := inputVal.Elem()
				for i := 0; i < v.NumField(); i++ {
					f := v.Field(i)
					if f.Type() == reflect.TypeFor[DynamicFrame]() {
						df = f.Addr().Interface().(*DynamicFrame)
						break
					}
				}
			}
			if df != nil {
				df.Columns = make(map[string]any)
				for name, arr := range columns {
					df.Columns[name] = arr
				}
			}
		} else {
			if err := bind(inputVal, targetStep.inputFieldsIndex, columns); err != nil {
				return baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("failed to bind input frame: %v", err),
				}
			}
		}
	}

	results := targetStep.Func.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		configVal.Elem(),
		inputVal,
		outputVal,
	})

	// 5. Handle output results and commit data to SHM.
	errResult := results[0]
	if !errResult.IsNil() {
		return baseplugin.ExecuteStepResponse{
			TaskID:       request.TaskID,
			Status:       baseplugin.StepResponseError,
			ErrorMessage: errResult.Interface().(error).Error(),
		}
	}

	// Check if the output is a VoidFrame (explicitly no-data).
	if targetStep.OutputType == reflect.TypeFor[VoidFrame]() {
		return baseplugin.ExecuteStepResponse{
			TaskID: request.TaskID,
			Status: baseplugin.StepResponseSuccess,
		}
	}

	outputHandles := make(map[string]string)
	dirtyHandles := make(map[string]string)

	if targetStep.OutputType == reflect.TypeFor[*DynamicFrame]() {
		var df *DynamicFrame
		if targetStep.OutputType == reflect.TypeFor[*DynamicFrame]() {
			df = outputVal.Interface().(*DynamicFrame)
		} else {
			v := outputVal.Elem()
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.Type() == reflect.TypeFor[DynamicFrame]() {
					df = f.Addr().Interface().(*DynamicFrame)
					break
				}
			}
		}
		if df != nil {
			for name, colData := range df.Columns {
				var arr arrow.Array
				var dirt []uint64

				switch d := colData.(type) {
				case *Int8:
					arr = d.arrayInt8
					dirt = d.dirt
				case *Int16:
					arr = d.arrayInt16
					dirt = d.dirt
				case *Int32:
					arr = d.arrayInt32
					dirt = d.dirt
				case *Int64:
					arr = d.arrayInt64
					dirt = d.dirt
				case *Uint8:
					arr = d.arrayUint8
					dirt = d.dirt
				case *Uint16:
					arr = d.arrayUint16
					dirt = d.dirt
				case *Uint32:
					arr = d.arrayUint32
					dirt = d.dirt
				case *Uint64:
					arr = d.arrayUint64
					dirt = d.dirt
				case *Float32:
					arr = d.arrayFloat32
					dirt = d.dirt
				case *Float64:
					arr = d.arrayFloat64
					dirt = d.dirt
				case *Bool:
					arr = d.arrayBool
					dirt = d.dirt
				case *String:
					arr = d.arrayString
					dirt = d.dirt
				case arrow.Array:
					arr = d
				default:
					return baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("unsupported dynamic output field type %T", colData),
					}
				}

				if arr != nil && !reflect.ValueOf(arr).IsNil() {
					path, err := locality.WriteArrowArrayOnlyToShm(arr)
					if err != nil {
						return baseplugin.ExecuteStepResponse{
							TaskID:       request.TaskID,
							Status:       baseplugin.StepResponseError,
							ErrorMessage: fmt.Sprintf("failed to write dynamic output frame: %v", err),
						}
					}
					outputHandles[name] = path

					if dirt != nil {
						hasDirty := false
						for _, b := range dirt {
							if b != 0 {
								hasDirty = true
								break
							}
						}
						if hasDirty {
							dp, err := locality.WriteDirtyToShm(dirt)
							if err != nil {
								logger.L().Error("Failed to write dirty bits to SHM", zap.Error(err))
							} else {
								dirtyHandles[name] = dp
							}
						}
					}
				}
			}
		}
	} else {
		vVal := outputVal.Elem()
		t := vVal.Type()

		for _, i := range targetStep.outputFieldsIndex {
			fValue := vVal.Field(i)
			fieldPtr := fValue.Interface()
			name := t.Field(i).Name

			var arr arrow.Array
			var dirt []uint64

			switch dataFrame := fieldPtr.(type) {
			case *Int8:
				arr = dataFrame.arrayInt8
				dirt = dataFrame.dirt
			case *Int16:
				arr = dataFrame.arrayInt16
				dirt = dataFrame.dirt
			case *Int32:
				arr = dataFrame.arrayInt32
				dirt = dataFrame.dirt
			case *Int64:
				arr = dataFrame.arrayInt64
				dirt = dataFrame.dirt
			case *Uint8:
				arr = dataFrame.arrayUint8
				dirt = dataFrame.dirt
			case *Uint16:
				arr = dataFrame.arrayUint16
				dirt = dataFrame.dirt
			case *Uint32:
				arr = dataFrame.arrayUint32
				dirt = dataFrame.dirt
			case *Uint64:
				arr = dataFrame.arrayUint64
				dirt = dataFrame.dirt
			case *Float32:
				arr = dataFrame.arrayFloat32
				dirt = dataFrame.dirt
			case *Float64:
				arr = dataFrame.arrayFloat64
				dirt = dataFrame.dirt
			case *Bool:
				arr = dataFrame.arrayBool
				dirt = dataFrame.dirt
			case *String:
				arr = dataFrame.arrayString
				dirt = dataFrame.dirt
			default:
				logger.L().Error("unsupported output field type", zap.String("type", fValue.Type().String()))
				return baseplugin.ExecuteStepResponse{
					TaskID:       request.TaskID,
					Status:       baseplugin.StepResponseError,
					ErrorMessage: fmt.Sprintf("unsupported output field type %s", fValue.Type()),
				}
			}

			if arr != nil && !reflect.ValueOf(arr).IsNil() {
				path, err := locality.WriteArrowArrayOnlyToShm(arr)
				if err != nil {
					return baseplugin.ExecuteStepResponse{
						TaskID:       request.TaskID,
						Status:       baseplugin.StepResponseError,
						ErrorMessage: fmt.Sprintf("failed to write output frame: %v", err),
					}
				} else {
					outputHandles[name] = path
				}

				hasDirty := false
				for _, d := range dirt {
					if d != 0 {
						hasDirty = true
						break
					}
				}
				if hasDirty {
					dp, err := locality.WriteDirtyToShm(dirt)
					if err != nil {
						logger.L().Error("Failed to write dirty bits to SHM", zap.Error(err))
					} else {
						dirtyHandles[name] = dp
					}
				}
			}
		}
	}

	return baseplugin.ExecuteStepResponse{
		TaskID:        request.TaskID,
		Status:        baseplugin.StepResponseSuccess,
		OutputHandles: outputHandles,
		DirtyHandles:  dirtyHandles,
	}
}

// ResolveSchema handles a request to resolve dynamic schemas for a specific step and configuration.
func (p *Plugin) ResolveSchema(req baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse {
	targetStep, ok := p.steps[req.StepName]
	if !ok {
		return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("step %s not found", req.StepName)}
	}

	configVal := reflect.New(targetStep.ConfigType)
	if req.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(req.ConfigJSON), configVal.Interface()); err != nil {
			return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("failed to unmarshal config: %v", err)}
		}
	}

	if resolver, ok := configVal.Interface().(TypeResolver); ok {
		input, output, err := resolver.ResolveTypes()
		if err != nil {
			return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("failed to resolve types: %v", err)}
		}
		return baseplugin.ResolveSchemaResponse{
			Input:  input,
			Output: output,
		}
	}

	return baseplugin.ResolveSchemaResponse{
		Input:  targetStep.InputSchema,
		Output: targetStep.OutputSchema,
	}
}

// ExecuteStepDirectly executes a registered step directly/locally (without starting gRPC/Arrow Flight, without SHM)
// using reflection, unmarshaling the configuration, injecting the resource by ID (if provided),
// and calling the step function.
func (p *Plugin) ExecuteStepDirectly(ctx context.Context, stepName string, configJSON string, resourceId string, input any, output any) error {
	step, ok := p.steps[stepName]
	if !ok {
		return fmt.Errorf("step %s not found in namespace %s", stepName, p.Namespace)
	}

	configVal := reflect.New(step.ConfigType)
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), configVal.Interface()); err != nil {
			return fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}

	// Inject resource if provided
	if resourceId != "" {
		p.activeResourcesMu.RLock()
		res, ok := p.activeResources[resourceId]
		p.activeResourcesMu.RUnlock()
		if ok {
			v := configVal.Elem()
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).Type() == reflect.TypeFor[Config]() {
					v.Field(i).Addr().Interface().(*Config).SetResource(res)
					break
				}
			}
		} else {
			return fmt.Errorf("active resource %s not found in namespace %s", resourceId, p.Namespace)
		}
	}

	// Call the step function. The step function takes the config struct by value,
	// and input/output structs by pointer.
	results := step.Func.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		configVal.Elem(),
		reflect.ValueOf(input),
		reflect.ValueOf(output),
	})

	errResult := results[0]
	if !errResult.IsNil() {
		return errResult.Interface().(error)
	}

	return nil
}

// InitializeResource instantiates a registered resource type, maps the provided configuration map,
// starts the resource, and registers it in the active resources map under the given ID.
func (p *Plugin) InitializeResource(ctx context.Context, id string, resourceTypeName string, config map[string]any) error {
	resReg, ok := p.resources[resourceTypeName]
	if !ok {
		return fmt.Errorf("resource type %q not registered in namespace %s", resourceTypeName, p.Namespace)
	}

	// Instantiate the registered type via reflect.New
	val := reflect.New(resReg.ResourceType)

	// Map configuration map[string]any if provided
	if config != nil {
		configBytes, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal configuration map for resource %q: %w", id, err)
		}
		if err := json.Unmarshal(configBytes, val.Interface()); err != nil {
			return fmt.Errorf("failed to unmarshal configuration for resource %q: %w", id, err)
		}
	}

	// Verify the instance implements the Resource interface
	resInstance, ok := val.Interface().(Resource)
	if !ok {
		return fmt.Errorf("resource type %q does not implement Resource interface", resourceTypeName)
	}

	// Start the resource
	if err := resInstance.Start(ctx); err != nil {
		return fmt.Errorf("failed to start resource %q: %w", id, err)
	}

	// Register in the active resources map
	p.activeResourcesMu.Lock()
	p.activeResources[id] = resInstance
	p.activeResourcesMu.Unlock()

	return nil
}

// bind maps Arrow Table columns to Go struct fields.
func bind(reflectValue reflect.Value, fieldIndices []int, columns map[string]arrow.Array) error {
	if reflectValue.Kind() != reflect.Pointer {
		return fmt.Errorf("type %v is not a pointer", reflectValue.Type())
	}

	v := reflectValue.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("type %v is not a struct", v.Type())
	}

	var numRows int = -1
	for _, arr := range columns {
		if numRows == -1 {
			numRows = arr.Len()
		} else if numRows != arr.Len() {
			return fmt.Errorf("inconsistent column lengths")
		}
	}
	if numRows == -1 {
		numRows = 0
	}

	t := v.Type()
	for _, i := range fieldIndices {
		fValue := v.Field(i)
		fieldPtr := fValue.Interface()

		name := t.Field(i).Name
		arr := columns[name]
		if arr == nil {
			return fmt.Errorf("column %q is required but missing", name)
		}

		switch df := fieldPtr.(type) {
		case *Int8:
			df.arrayInt8 = arr.(*array.Int8)
			df.dirt = []uint64{}
		case *Int16:
			df.arrayInt16 = arr.(*array.Int16)
			df.dirt = []uint64{}
		case *Int32:
			df.arrayInt32 = arr.(*array.Int32)
			df.dirt = []uint64{}
		case *Int64:
			df.arrayInt64 = arr.(*array.Int64)
			df.dirt = []uint64{}
		case *Uint8:
			df.arrayUint8 = arr.(*array.Uint8)
			df.dirt = []uint64{}
		case *Uint16:
			df.arrayUint16 = arr.(*array.Uint16)
			df.dirt = []uint64{}
		case *Uint32:
			df.arrayUint32 = arr.(*array.Uint32)
			df.dirt = []uint64{}
		case *Uint64:
			df.arrayUint64 = arr.(*array.Uint64)
			df.dirt = []uint64{}
		case *Float32:
			df.arrayFloat32 = arr.(*array.Float32)
			df.dirt = []uint64{}
		case *Float64:
			df.arrayFloat64 = arr.(*array.Float64)
			df.dirt = []uint64{}
		case *Bool:
			df.arrayBool = arr.(*array.Boolean)
			df.dirt = []uint64{}
		case *String:
			df.arrayString = arr.(*array.String)
			df.dirt = []uint64{}
		default:
			return fmt.Errorf("field name '%s' has unsupported type %v", name, fValue.Type())
		}
	}

	return nil
}

func extractMetadata(fnPtr uintptr) (doc string, code string, file string, line int) {
	f := runtime.FuncForPC(fnPtr)
	if f == nil {
		return
	}
	file, line = f.FileLine(fnPtr)

	// Try to read the source file
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}

	fset := token.NewFileSet()
	// Parse the file to get AST and comments
	node, err := parser.ParseFile(fset, file, data, parser.ParseComments)
	if err != nil {
		return
	}

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Check if this function corresponds to our line
			startLine := fset.Position(d.Pos()).Line
			endLine := fset.Position(d.End()).Line
			if startLine <= line && endLine >= line {
				if d.Doc != nil {
					doc = d.Doc.Text()
				}
				// Extract source code of the function
				start := fset.Position(d.Pos()).Offset
				end := fset.Position(d.End()).Offset
				code = string(data[start:end])
				return
			}
		case *ast.GenDecl:
			// Handle types (Resources)
			for _, spec := range d.Specs {
				if tSpec, ok := spec.(*ast.TypeSpec); ok {
					startLine := fset.Position(tSpec.Pos()).Line
					endLine := fset.Position(tSpec.End()).Line
					if startLine <= line && endLine >= line {
						if d.Doc != nil {
							doc = d.Doc.Text()
						}
						// Extract source code of the type declaration
						start := fset.Position(d.Pos()).Offset
						end := fset.Position(d.End()).Offset
						code = string(data[start:end])
						return
					}
				}
			}
		}
	}
	return
}

// New creates a new Heddle Plugin instance within the specified namespace.
func New(namespace string) *Plugin {
	return &Plugin{
		Namespace:       namespace,
		Language:        "go",
		resources:       make(map[string]resourceRegistration),
		steps:           make(map[string]StepRegistration),
		Ready:           make(chan struct{}),
		activeResources: make(map[string]Resource),
	}
}
