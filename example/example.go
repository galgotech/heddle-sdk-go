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

func (r *Connection) Init(ctx context.Context) error {
	return nil
}

func (r *Connection) Close() error {
	return nil
}

func (r *Connection) query(ctx context.Context, query string) ([]int64, error) {
	return []int64{1}, nil
}

type TableInput struct {
	Query *schema.ColString
}

type TestOutput struct {
	Test  *schema.ColString
	Test2 *schema.ColString
}

type TableOutput struct {
	RowsAffected   *schema.ColInt64
	DataTest       *schema.ColInt64
	DataTest2      *schema.ColInt64
	MultipleStrucs *schema.ColStruct[TestOutput]
}

type Config struct {
	Query string
}

type Steps struct {
	OtherValue string
	DB         schema.Resource[*Connection]
}

func (s *Steps) ResolveTypeInput(ctx context.Context, config Config, stepName string) ([]schema.ColSchema, error) {
	return []schema.ColSchema{
		{
			Type: "string",
			Name: "Query",
		},
	}, nil
}

func (s *Steps) ResolveTypeOutput(ctx context.Context, config Config, stepName string) ([]schema.ColSchema, error) {
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

	t := []*TestOutput{
		{
			Test:  schema.NewColString([]string{"123"}),
			Test2: schema.NewColString([]string{"123"}),
		},
		{
			Test:  schema.NewColString([]string{"123"}),
			Test2: schema.NewColString([]string{"123"}),
		},
	}

	output := &TableOutput{
		RowsAffected:   schema.NewColInt64(data),
		MultipleStrucs: schema.NewColStruct(t),
	}
	return output, nil
}

func (s *Steps) Step2(ctx context.Context, config Config, in *schema.Any) (*schema.Any, error) {
	data, err := s.DB.Get().query(ctx, config.Query)
	if err != nil {
		return nil, err
	}

	output := schema.NewAny(map[string]any{
		"RowsAffected": data,
	})
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

	steps := &Steps{}
	err := p.Register(steps)
	if err != nil {
		logger.L().Error("Failed to register steps", zap.Error(err))
	}

	c := Config{
		Query: "select 1",
	}
	input := TableInput{
		Query: schema.NewColString([]string{"123"}),
	}

	ctx := context.Background()
	output := p.Execute(ctx, "step1", c, input)
	fmt.Println(output)
}
