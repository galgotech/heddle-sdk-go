package executor

import (
	"github.com/galgotech/heddle-sdk-go/internal/executor/execute"
)

func PackData(input any) *execute.StepReference {
	return execute.PackData(input)
}

func UnpackData(ref any) any {
	return execute.UnpackData(ref)
}
