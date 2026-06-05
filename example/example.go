package example

//go:generate go run github.com/galgotech/heddle-sdk-go/cmd/heddle-gen -struct Steps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/local"
	"github.com/galgotech/heddle-sdk-go/network"
	"github.com/galgotech/heddle-sdk-go/plugin"
	"github.com/galgotech/heddle-sdk-go/schema"
)

// Connection represents the `pg.connection` resource definition.
type Connection struct {
	Host string
}

func (r *Connection) Init(ctx context.Context) error {
	logger.L().Info("Initializing PostgreSQL Connection resource", zap.String("host", r.Host))
	return nil
}

func (r *Connection) Close() error {
	logger.L().Info("Closing PostgreSQL Connection resource", zap.String("host", r.Host))
	return nil
}

type SubInput struct {
	Name string
}

// QueryInput represents the input columnar structure containing inputs like `@user_id`.
type QueryInput struct {
	UserID int64

	SubInput schema.FrameInput[SubInput]
}

// QueryOutput represents the returned output frame schemas.
type QueryOutput struct {
	UserID  int64
	Country string
}

// Steps groups all step methods and binds stateful resources.
type Steps struct {
	SQL string
	DB  schema.ResourceSchema[*Connection]
}

func (s *Steps) TestProducer(ctx context.Context, out schema.FrameOutput[QueryOutput]) error {
	queryOutput := QueryOutput{
		UserID:  1,
		Country: "Brasil",
	}
	out.Add(queryOutput)

	return nil
}

// Query implements the `pg.query` step behavior.
func (s *Steps) Query(ctx context.Context, in schema.FrameInput[QueryInput], out schema.FrameOutput[QueryOutput]) error {

	conn := s.DB.Get()
	if conn == nil {
		return fmt.Errorf("PostgreSQL connection DB resource is not initialized or bound!")
	}

	in.Each(func(item QueryInput) error {
		output := QueryOutput{}
		output.UserID = item.UserID
		output.Country = fmt.Sprintf("US (resolved via %s)", conn.Host)

		out.Add(output)
		item.SubInput.Each(func(subItem SubInput) error {
			_ = subItem.Name
			return nil
		})
		return nil
	})

	return nil
}

// Start registers step dependencies and boots up standard network/gRPC Worker coordination.
func Start() {
	p := plugin.New("pg")

	err := RegisterSteps(p)
	if err != nil {
		logger.L().Error("Failed to register steps", zap.Error(err))
	}

	go network.Run(context.Background(), p)
}

// Run executes the processing flow locally in-process without worker daemon dependencies.
func Run() {
	p := plugin.New("pg")

	err := RegisterSteps(p)
	if err != nil {
		logger.L().Error("Failed to register steps", zap.Error(err))
	}

	// resource test = pg.connection { host: "pg.internal" }
	configResource := map[string]any{"host": "pg.internal"}

	err = p.ResourceInstance("d_b", "d_b", configResource)
	if err != nil {
		logger.L().Error("Failed to create resource instance", zap.Error(err))
	}

	// pg.query { sql: "SELECT id AS user_id, country FROM users WHERE id = @user_id" }
	c := map[string]any{
		"SQL": "SELECT id AS user_id, country FROM users WHERE id = @user_id",
	}
	configBytes, _ := json.Marshal(c)

	inBuilder := array.NewInt64Builder(memory.DefaultAllocator)
	defer inBuilder.Release()
	inBuilder.Append(123)

	inputCols := map[string]arrow.Array{
		"UserID": inBuilder.NewArray(),
	}

	ctx := context.Background()
	exec := local.NewLocalRunner(p)
	output := exec.Execute(ctx, "query", string(configBytes), inputCols)

	fmt.Printf("\n--- Step Direct Execution Result ---\n")

	if outIDArr, ok := output["UserID"].(*array.Int64); ok {
		if outCountryArr, ok := output["Country"].(*array.String); ok {
			for i := 0; i < outIDArr.Len(); i++ {
				fmt.Printf("Row %d: UserID=%d, Country=%s\n", i, outIDArr.Value(i), outCountryArr.Value(i))
			}
		}
	}

	fmt.Printf("-------------------------------------\n\n")
}
