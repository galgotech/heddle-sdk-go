package example

import (
	"context"
	"fmt"

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

// QueryConfig represents the step configuration (`query` parameter in Heddle Step pg.query).
type QueryConfig struct {
	Query string `json:"query"`
}

type SubInput struct {
	Name string
}

// QueryInput represents the input columnar structure containing inputs like `@user_id`.
type QueryInput struct {
	UserID int64

	SubInput schema.Frame[SubInput]
}

// QueryOutput represents the returned output frame schemas.
type QueryOutput struct {
	UserID  int64
	Country string
}

// Steps groups all step methods and binds stateful resources.
type Steps struct {
	DB schema.ResourceSchema[*Connection]
}

func (s *Steps) TestProducer(ctx context.Context, config QueryConfig, in schema.Void, out schema.Frame[QueryOutput]) error {
	queryOutput := QueryOutput{
		UserID:  1,
		Country: "Brasil",
	}
	out.Add(queryOutput)

	return nil
}

// Query implements the `pg.query` step behavior.
func (s *Steps) Query(ctx context.Context, config QueryConfig, in schema.Frame[QueryInput], out schema.Frame[QueryOutput]) error {
	conn := s.DB.Get()
	if conn == nil {
		return fmt.Errorf("PostgreSQL connection DB resource is not initialized or bound!")
	}

	in.Each(func(item QueryInput) {
		output := QueryOutput{}
		output.UserID = item.UserID
		output.Country = fmt.Sprintf("US (resolved via %s)", conn.Host)

		out.Add(output)
		item.SubInput.Each(func(subItem SubInput) {
			_ = subItem.Name
		})
	})

	return nil
}

// Start registers step dependencies and boots up standard network/gRPC Worker coordination.
func Start() {
	p := plugin.New("pg")

	steps := &Steps{}

	err := p.Register(steps)
	if err != nil {
		logger.L().Error("Failed to register steps", zap.Error(err))
	}

	go network.Run(context.Background(), p)
}

// Run executes the processing flow locally in-process without worker daemon dependencies.
func Run() {
	p := plugin.New("pg")

	steps := &Steps{}

	err := p.Register(steps)
	if err != nil {
		logger.L().Error("Failed to register steps", zap.Error(err))
	}

	// resource test = pg.connection { host: "pg.internal" }
	configResource := map[string]any{"host": "pg.internal"}

	err = p.ResourceInstance("DB", "pg.connection", configResource)
	if err != nil {
		logger.L().Error("Failed to create resource instance", zap.Error(err))
	}

	// step fetch_user_data = <DB=test> ...
	// p.ResourceSet("DB", "pg.test")

	// pg.query { query: "SELECT id AS user_id, country FROM users WHERE id = @user_id" }
	c := QueryConfig{
		Query: "SELECT id AS user_id, country FROM users WHERE id = @user_id",
	}

	input := QueryInput{
		UserID: 123,
	}

	ref, _ := schema.NewFrame(nil, []QueryInput{input})

	ctx := context.Background()
	exec := local.NewLocalRunner(p)
	output := exec.Execute(ctx, "query", c, ref)

	fmt.Printf("\n--- Step Direct Execution Result ---\n")

	if out, ok := output.(schema.Frame[QueryOutput]); ok {
		rowIdx := 0

		out.Each(func(item QueryOutput) {
			fmt.Printf("Row %d: UserID=%d, Country=%s\n", rowIdx, item.UserID, item.Country)
			rowIdx++
		})
	}

	fmt.Printf("-------------------------------------\n\n")
}
