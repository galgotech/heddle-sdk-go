package schema

import (
	"fmt"
	"strings"
)

// printFrame prints a DynamicFrame beautifully as a table.
func PrintFrame(df *DynamicFrame) {
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
			case *Int8:
				numRows = col.Len()
			case *Int16:
				numRows = col.Len()
			case *Int32:
				numRows = col.Len()
			case *Int64:
				numRows = col.Len()
			case *Uint8:
				numRows = col.Len()
			case *Uint16:
				numRows = col.Len()
			case *Uint32:
				numRows = col.Len()
			case *Uint64:
				numRows = col.Len()
			case *Float32:
				numRows = col.Len()
			case *Float64:
				numRows = col.Len()
			case *Bool:
				numRows = col.Len()
			case *String:
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
			case *Int8:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Int16:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Int32:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Int64:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Uint8:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Uint16:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Uint32:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Uint64:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%d", col.Value(i))
				}
			case *Float32:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%.4f", col.Value(i))
				}
			case *Float64:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%.4f", col.Value(i))
				}
			case *Bool:
				if col.IsNull(i) {
					valStr = "NULL"
				} else {
					valStr = fmt.Sprintf("%t", col.Value(i))
				}
			case *String:
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
			fmt.Fprintf(&rowBuilder, "| %-*s ", fmtWidth, rowStrings[i][name])
		}
		rowBuilder.WriteString("|")
		fmt.Println(rowBuilder.String())
	}
	fmt.Println(separatorBuilder.String())
}
