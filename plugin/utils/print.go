package utils

import (
	"fmt"
	"strings"

	"github.com/galgotech/heddle-lang/sdk/go/plugin"
)

// printFrame prints a DynamicFrame beautifully as a table.
func PrintFrame(df *plugin.DynamicFrame) {
	if df == nil || len(df.Columns) == 0 {
		fmt.Println("Empty result frame.")
		return
	}

	// 1. Get ordered column names and calculate column widths
	var colNames []string
	var numRows int = 0

	for name, colData := range df.Columns {
		colNames = append(colNames, name)
		if numRows == 0 {
			switch col := colData.(type) {
			case *plugin.Int8:
				numRows = col.Len()
			case *plugin.Int16:
				numRows = col.Len()
			case *plugin.Int32:
				numRows = col.Len()
			case *plugin.Int64:
				numRows = col.Len()
			case *plugin.Uint8:
				numRows = col.Len()
			case *plugin.Uint16:
				numRows = col.Len()
			case *plugin.Uint32:
				numRows = col.Len()
			case *plugin.Uint64:
				numRows = col.Len()
			case *plugin.Float32:
				numRows = col.Len()
			case *plugin.Float64:
				numRows = col.Len()
			case *plugin.Bool:
				numRows = col.Len()
			case *plugin.String:
				numRows = col.Len()
			}
		}
	}

	// Calculate maximum width for each column to align nicely
	widths := make(map[string]int)
	for _, name := range colNames {
		widths[name] = len(name)
		if widths[name] < 10 { // Ensure a minimum width of 10
			widths[name] = 10
		}
	}

	// Pre-format rows to calculate actual string widths
	rowStrings := make([]map[string]string, numRows)
	for i := 0; i < numRows; i++ {
		rowStrings[i] = make(map[string]string)
		for _, name := range colNames {
			colData := df.Columns[name]
			var valStr string
			switch col := colData.(type) {
			case *plugin.Int8:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Int16:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Int32:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Int64:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Uint8:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Uint16:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Uint32:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Uint64:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *plugin.Float32:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%.4f", col.Value(i))
				}
			case *plugin.Float64:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%.4f", col.Value(i))
				}
			case *plugin.Bool:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%t", col.Value(i))
				}
			case *plugin.String:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = col.Value(i)
				}
			default:
				valStr = fmt.Sprintf("%v", colData)
			}
			rowStrings[i][name] = valStr
			if len(valStr) > widths[name] {
				widths[name] = len(valStr)
			}
		}
	}

	// 2. Print header
	var headerBuilder strings.Builder
	var separatorBuilder strings.Builder
	for _, name := range colNames {
		fmtWidth := widths[name]
		headerBuilder.WriteString(fmt.Sprintf("| %-*s ", fmtWidth, name))
		separatorBuilder.WriteString(fmt.Sprintf("+-%s-", strings.Repeat("-", fmtWidth)))
	}
	headerBuilder.WriteString("|")
	separatorBuilder.WriteString("+")

	fmt.Println(separatorBuilder.String())
	fmt.Println(headerBuilder.String())
	fmt.Println(separatorBuilder.String())

	// 3. Print rows
	for i := 0; i < numRows; i++ {
		var rowBuilder strings.Builder
		for _, name := range colNames {
			fmtWidth := widths[name]
			rowBuilder.WriteString(fmt.Sprintf("| %-*s ", fmtWidth, rowStrings[i][name]))
		}
		rowBuilder.WriteString("|")
		fmt.Println(rowBuilder.String())
	}
	fmt.Println(separatorBuilder.String())
}
