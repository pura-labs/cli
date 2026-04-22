package output

import (
	"fmt"
	"io"
)

// PrintTable renders a simple fixed-width table to w.
// Column widths are calculated automatically from headers and data.
func PrintTable(w io.Writer, headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	// Calculate column widths
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i := 0; i < len(row) && i < len(widths); i++ {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	// Print headers
	for i, h := range headers {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		fmt.Fprintf(w, "%-*s", widths[i], h)
	}
	fmt.Fprintln(w)

	// Print separator
	for i, width := range widths {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		for j := 0; j < width; j++ {
			fmt.Fprint(w, "-")
		}
	}
	fmt.Fprintln(w)

	// Print rows
	for _, row := range rows {
		for i := 0; i < len(headers); i++ {
			if i > 0 {
				fmt.Fprint(w, "  ")
			}
			val := ""
			if i < len(row) {
				val = row[i]
			}
			fmt.Fprintf(w, "%-*s", widths[i], val)
		}
		fmt.Fprintln(w)
	}
}
