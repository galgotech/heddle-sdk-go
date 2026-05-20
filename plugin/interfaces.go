package plugin

import (
	"context"

	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
)

type Registry interface {
	RegisterResource(name string, resource any) error
	RegisterStep(name string, fn any) error
	ResolveSchema(req baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse
	GetStep(name string) (StepRegistration, bool)
	GetResource(name string) (resourceRegistration, bool)
	AllSteps() map[string]StepRegistration
	AllResources() map[string]resourceRegistration
}

type Executor interface {
	ExecuteTask(ctx context.Context, request baseplugin.ExecuteStepRequest) baseplugin.ExecuteStepResponse
	ExecuteStepDirectly(ctx context.Context, stepName string, configJSON string, resourceId string, input any, output any) error
}

type NetworkClient interface {
	Start(ctx context.Context) error
}
