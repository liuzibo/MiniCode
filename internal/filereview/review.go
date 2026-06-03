package filereview

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Permission interface {
	EnsureEdit(ctx context.Context, targetPath, diffPreview string) error
}

type Result struct {
	OK     bool
	Output string
}

func BuildUnifiedDiff(filePath, before, after string) string {
	if before == after {
		return "(no changes for " + filePath + ")"
	}

	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	ops := diffOps(beforeLines, afterLines)
	hunks := groupHunks(ops, 3)

	var builder strings.Builder
	builder.WriteString("--- a/" + filePath + "\n")
	builder.WriteString("+++ b/" + filePath + "\n")
	for _, hunk := range hunks {
		oldStart, oldCount, newStart, newCount := hunkRangeForOps(hunk)
		builder.WriteString("@@ -" + hunkRange(oldStart, oldCount) + " +" + hunkRange(newStart, newCount) + " @@\n")
		for _, op := range hunk {
			builder.WriteString(op.kind + op.line)
		}
	}
	return builder.String()
}

type diffOp struct {
	kind    string
	line    string
	oldLine int
	newLine int
}

func diffOps(beforeLines, afterLines []string) []diffOp {
	lcs := make([][]int, len(beforeLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(afterLines)+1)
	}
	for i := len(beforeLines) - 1; i >= 0; i-- {
		for j := len(afterLines) - 1; j >= 0; j-- {
			if beforeLines[i] == afterLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	ops := []diffOp{}
	i, j := 0, 0
	oldLine, newLine := 1, 1
	for i < len(beforeLines) || j < len(afterLines) {
		switch {
		case i < len(beforeLines) && j < len(afterLines) && beforeLines[i] == afterLines[j]:
			ops = append(ops, diffOp{kind: " ", line: beforeLines[i], oldLine: oldLine, newLine: newLine})
			i++
			j++
			oldLine++
			newLine++
		case j < len(afterLines) && (i == len(beforeLines) || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, diffOp{kind: "+", line: afterLines[j], oldLine: oldLine, newLine: newLine})
			j++
			newLine++
		default:
			ops = append(ops, diffOp{kind: "-", line: beforeLines[i], oldLine: oldLine, newLine: newLine})
			i++
			oldLine++
		}
	}
	return ops
}

func groupHunks(ops []diffOp, context int) [][]diffOp {
	changeIndexes := []int{}
	for index, op := range ops {
		if op.kind != " " {
			changeIndexes = append(changeIndexes, index)
		}
	}
	if len(changeIndexes) == 0 {
		return nil
	}
	hunks := [][]diffOp{}
	nextChange := 0
	for nextChange < len(changeIndexes) {
		start := changeIndexes[nextChange] - context
		if start < 0 {
			start = 0
		}
		end := changeIndexes[nextChange] + context + 1
		nextChange++
		for nextChange < len(changeIndexes) && changeIndexes[nextChange] < end+context {
			end = changeIndexes[nextChange] + context + 1
			nextChange++
		}
		if end > len(ops) {
			end = len(ops)
		}
		hunks = append(hunks, ops[start:end])
	}
	return hunks
}

func hunkRangeForOps(ops []diffOp) (int, int, int, int) {
	if len(ops) == 0 {
		return 1, 0, 1, 0
	}
	oldStart := ops[0].oldLine
	newStart := ops[0].newLine
	oldCount, newCount := 0, 0
	for _, op := range ops {
		if op.kind != "+" {
			oldCount++
		}
		if op.kind != "-" {
			newCount++
		}
	}
	if oldCount == 0 && oldStart > 1 {
		oldStart--
	}
	if newCount == 0 && newStart > 1 {
		newStart--
	}
	return oldStart, oldCount, newStart, newCount
}

func ApplyReviewedChange(ctx context.Context, permission Permission, filePath, targetPath, nextContent string) Result {
	previousContent := ""
	if bytes, err := os.ReadFile(targetPath); err == nil {
		previousContent = string(bytes)
	} else if !os.IsNotExist(err) {
		return Result{OK: false, Output: err.Error()}
	}

	if previousContent == nextContent {
		return Result{OK: true, Output: "No changes needed for " + filePath}
	}

	diff := BuildUnifiedDiff(filePath, previousContent, nextContent)
	if permission != nil {
		if err := permission.EnsureEdit(ctx, targetPath, diff); err != nil {
			return Result{OK: false, Output: err.Error()}
		}
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return Result{OK: false, Output: err.Error()}
	}
	if err := os.WriteFile(targetPath, []byte(nextContent), 0o644); err != nil {
		return Result{OK: false, Output: err.Error()}
	}
	return Result{OK: true, Output: "Applied reviewed changes to " + filePath}
}

func splitLines(input string) []string {
	if input == "" {
		return nil
	}
	lines := strings.SplitAfter(input, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func hunkRange(start, count int) string {
	if count == 0 {
		return strconv.Itoa(start) + ",0"
	}
	if count <= 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}
