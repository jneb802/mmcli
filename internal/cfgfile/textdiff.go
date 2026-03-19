package cfgfile

import (
	"fmt"
	"strings"
)

// TextDiff compares two byte slices line-by-line and returns a unified diff string.
// Used for .yaml, .yml, .json, and other non-.cfg config files.
func TextDiff(nameA, nameB string, a, b []byte) string {
	linesA := strings.Split(string(a), "\n")
	linesB := strings.Split(string(b), "\n")

	// Compute LCS table
	m, n := len(linesA), len(linesB)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if linesA[i] == linesB[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	// Generate diff hunks from LCS
	type diffLine struct {
		op   byte // ' ', '-', '+'
		text string
		numA int // 1-based line number in A (0 if op='+')
		numB int // 1-based line number in B (0 if op='-')
	}

	var all []diffLine
	i, j := 0, 0
	for i < m && j < n {
		if linesA[i] == linesB[j] {
			all = append(all, diffLine{' ', linesA[i], i + 1, j + 1})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			all = append(all, diffLine{'-', linesA[i], i + 1, 0})
			i++
		} else {
			all = append(all, diffLine{'+', linesB[j], 0, j + 1})
			j++
		}
	}
	for ; i < m; i++ {
		all = append(all, diffLine{'-', linesA[i], i + 1, 0})
	}
	for ; j < n; j++ {
		all = append(all, diffLine{'+', linesB[j], 0, j + 1})
	}

	// Check if there are any changes
	hasChanges := false
	for _, d := range all {
		if d.op != ' ' {
			hasChanges = true
			break
		}
	}
	if !hasChanges {
		return ""
	}

	// Build unified diff with context (3 lines)
	const contextLines = 3
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n", nameA)
	fmt.Fprintf(&out, "+++ %s\n", nameB)

	// Find hunk boundaries
	type hunkRange struct {
		start, end int // indices into all
	}
	var hunks []hunkRange

	for idx, d := range all {
		if d.op != ' ' {
			start := idx - contextLines
			if start < 0 {
				start = 0
			}
			end := idx + contextLines + 1
			if end > len(all) {
				end = len(all)
			}

			if len(hunks) > 0 && start <= hunks[len(hunks)-1].end {
				// Merge with previous hunk
				hunks[len(hunks)-1].end = end
			} else {
				hunks = append(hunks, hunkRange{start, end})
			}
		}
	}

	for _, h := range hunks {
		// Calculate line ranges for the hunk header
		startA, countA := 0, 0
		startB, countB := 0, 0
		for idx := h.start; idx < h.end; idx++ {
			d := all[idx]
			if d.op == ' ' || d.op == '-' {
				if startA == 0 {
					startA = d.numA
				}
				countA++
			}
			if d.op == ' ' || d.op == '+' {
				if startB == 0 {
					startB = d.numB
				}
				countB++
			}
		}
		if startA == 0 {
			startA = 1
		}
		if startB == 0 {
			startB = 1
		}

		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", startA, countA, startB, countB)
		for idx := h.start; idx < h.end; idx++ {
			d := all[idx]
			fmt.Fprintf(&out, "%c%s\n", d.op, d.text)
		}
	}

	return out.String()
}
