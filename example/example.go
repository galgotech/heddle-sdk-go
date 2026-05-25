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

// QueryInput represents the input columnar structure containing inputs like `@user_id`.
type QueryInput struct {
	UserID *schema.ColString
}

// QueryOutput represents the returned output frame schemas.
type QueryOutput struct {
	UserID  *schema.ColInt64
	Country *schema.ColString
}

// Steps groups all step methods and binds stateful resources.
type Steps struct {
	DB schema.Resource[*Connection]
}

// Query implements the `pg.query` step behavior.
func (s *Steps) Query(ctx context.Context, config QueryConfig, in *QueryInput) *QueryOutput {
	conn := s.DB.Get()
	if conn == nil {
		logger.L().Error("PostgreSQL connection DB resource is not initialized or bound!")
		return &QueryOutput{}
	}

	numRows := in.UserID.Len()
	userIDs := make([]int64, numRows)
	countries := make([]string, numRows)

	for i := 0; i < numRows; i++ {
		idStr := in.UserID.Value(i)
		var id int64
		_, _ = fmt.Sscan(idStr, &id)
		if id == 0 {
			id = int64(i + 1)
		}
		userIDs[i] = id
		// Map connection host to mock country for demo
		countries[i] = fmt.Sprintf("US (resolved via %s)", conn.Host)
	}

	return &QueryOutput{
		UserID:  schema.NewColInt64(userIDs),
		Country: schema.NewColString(countries),
	}
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
	configResource := map[string]string{"host": "pg.internal"}
	err = p.ResourceInstance("test", "pg.connection", configResource)
	if err != nil {
		logger.L().Error("Failed to create resource instance", zap.Error(err))
	}

	// step fetch_user_data = <DB=test> ...
	p.ResourceSet("DB", "pg.test")

	// pg.query { query: "SELECT id AS user_id, country FROM users WHERE id = @user_id" }
	c := QueryConfig{
		Query: "SELECT id AS user_id, country FROM users WHERE id = @user_id",
	}
	input := QueryInput{
		UserID: schema.NewColString([]string{"123", "456"}),
	}

	ctx := context.Background()
	exec := local.NewLocalRunner(p)
	output := exec.Execute(ctx, "query", c, &input)
	fmt.Printf("\n--- Step Direct Execution Result ---\n")
	if out, ok := output.(*QueryOutput); ok {
		for i := 0; i < out.UserID.Len(); i++ {
			fmt.Printf("Row %d: UserID=%d, Country=%s\n", i, out.UserID.Value(i), out.Country.Value(i))
		}
	}
	fmt.Printf("-------------------------------------\n\n")
}
