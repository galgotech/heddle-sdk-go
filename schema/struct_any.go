package schema

import (
	"github.com/apache/arrow/go/v18/arrow"
	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	internalarrow "github.com/galgotech/heddle-sdk-go/internal/arrow"
)

type Any struct {
	columnsArr map[string]arrow.Array
	idsArray   map[string]arrow.Array
}

func (a *Any) Get(name string) (arrow.Array, bool) {
	value, ok := a.columnsArr[name]
	return value, ok
}

func (a *Any) Columns() []string {
	columns := make([]string, 0, len(a.columnsArr))
	for key := range a.columnsArr {
		columns = append(columns, key)
	}
	return columns
}

func NewAny(data map[string]any) *Any {
	typeAny := &Any{
		columnsArr: make(map[string]arrow.Array, len(data)),
		idsArray:   make(map[string]arrow.Array, len(data)),
	}

	for key, value := range data {
		ids := make([]int64, len(value.([]any)))
		for i := range ids {
			ids[i] = colIDNode.Generate().Int64()
		}

		arr, err := internalarrow.SliceToArrowArray(value)
		if err != nil {
			logger.L().Fatal("failed to convert slice to arrow array", zap.Error(err))
		}
		typeAny.columnsArr[key] = arr
	}

	return typeAny
}

func NewAnyAccessor(token accessor.Token, columns map[string]arrow.Array, ids map[string]arrow.Array) *Any {
	typeAny := &Any{
		columnsArr: columns,
		idsArray:   ids,
	}

	return typeAny
}
