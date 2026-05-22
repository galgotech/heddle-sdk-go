package example

import (
	"context"
	"fmt"

	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/plugin"
	"github.com/galgotech/heddle-sdk-go/schema"
)

type Connection struct {
	Host string
}

func (r *Connection) Start(ctx context.Context) error {
	return nil
}

func (r *Connection) Close() error {
	return nil
}

func (r *Connection) query(ctx context.Context, query string) ([]int64, error) {
	return []int64{1}, nil
}

type TableInput struct {
	Query schema.Col[string]
}

type TableOutput struct {
	RowsAffected schema.Col[int64]
}

type Config struct {
	Query string
}

type Steps struct {
	OtherValue string
	DB         schema.Resource[*Connection]
}

func (s *Steps) ResolveInput(ctx context.Context, config Config, stepName string) ([]schema.ColSchema, error) {
	return []schema.ColSchema{
		{
			Type: "string",
			Name: "Query",
		},
	}, nil
}

func (s *Steps) ResolveOutput(ctx context.Context, config Config, stepName string) ([]schema.ColSchema, error) {
	return []schema.ColSchema{
		{
			Type: "int64",
			Name: "RowsAffected",
		},
	}, nil
}

func (s *Steps) Step1(ctx context.Context, config Config, in *TableInput) (*TableOutput, error) {
	data, err := s.DB.Get().query(ctx, config.Query)
	if err != nil {
		return nil, err
	}
	output := &TableOutput{
		RowsAffected: schema.NewCol(data),
	}
	return output, nil
}

func (s *Steps) Step2(ctx context.Context, config Config, in *schema.Any) (*schema.Any, error) {
	data, err := s.DB.Get().query(ctx, config.Query)
	if err != nil {
		return nil, err
	}

	output := &schema.Any{}
	output.Set("RowsAffected", schema.NewCol(data))
	return output, nil
}

func Start() {
	p := plugin.New("ns1")

	steps := &Steps{
		OtherValue: "123",
	}
	err := p.Register(steps)
	if err != nil {
		logger.L().Error("Failed to register steps", zap.Error(err))
	}

	go p.Start()
}

func Run() {
	p := plugin.New("ns1")

	steps := &Steps{
		OtherValue: "123",
		DB: schema.Resource[*Connection]{
			Resource: &Connection{
				Host: "localhost:5432",
			},
		},
	}

	err := p.Register(steps)
	if err != nil {
		logger.L().Error("Failed to register steps", zap.Error(err))
	}

	c := Config{
		Query: "select 1",
	}
	input := TableInput{
		Query: schema.NewCol([]string{"123"}),
	}

	ctx := context.Background()
	output := p.Execute(ctx, "step1", c, input)
	fmt.Println(output)
}
