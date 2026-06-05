package example

//go:generate go run github.com/galgotech/heddle-sdk-go/cmd/heddle-gen -struct Steps

import (
	"context"
	"fmt"

	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

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

	if err := network.Run(context.Background(), p); err != nil {
		logger.L().Fatal("Failed to run network", zap.Error(err))
	}
}
