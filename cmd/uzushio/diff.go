package main

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines are shown either side of a change.
// Three is what every unified diff shows, and the point here is that the
// output is readable by someone who has read one before.
const diffContext = 3

// op is one line of the comparison: kept, removed from the file on disk, or
// added by the generator.
type op struct {
	kind byte // ' ', '-' or '+'
	text string
}

// diff renders the difference between the file at path and the generated
// bytes as unified-ish hunks. It exists rather than shelling out to diff(1)
// because a check that only runs where a particular diff is installed is a
// check that silently does not run.
//
// path is carried into the header rather than assumed: --out names the file,
// and a header naming a different one would send the reader to the wrong
// place.
func diff(path, onDisk, generated string) []string {
	before := splitLines(onDisk)
	after := splitLines(generated)
	ops := align(before, after)
	out := []string{"--- " + path + " (on disk)", "+++ " + path + " (generated)"}
	return append(out, hunks(ops)...)
}

// splitLines splits into lines, dropping the empty piece a trailing newline
// leaves behind so a file that ends properly does not read as one line longer
// than it is.
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// align turns two line lists into the edit script that walks from one to the
// other, keeping as many lines as possible. It is the textbook longest common
// subsequence: the files being compared are two renderings of one
// configuration, a few hundred lines each, so the quadratic table is cheaper
// than any cleverness would be to read.
func align(before, after []string) []op {
	rows, cols := len(before), len(after)
	table := make([][]int, rows+1)
	for i := range table {
		table[i] = make([]int, cols+1)
	}
	for i := rows - 1; i >= 0; i-- {
		for j := cols - 1; j >= 0; j-- {
			if before[i] == after[j] {
				table[i][j] = table[i+1][j+1] + 1
				continue
			}
			table[i][j] = max(table[i+1][j], table[i][j+1])
		}
	}
	var ops []op
	i, j := 0, 0
	for i < rows && j < cols {
		switch {
		case before[i] == after[j]:
			ops = append(ops, op{kind: ' ', text: before[i]})
			i, j = i+1, j+1
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, op{kind: '-', text: before[i]})
			i++
		default:
			ops = append(ops, op{kind: '+', text: after[j]})
			j++
		}
	}
	for ; i < rows; i++ {
		ops = append(ops, op{kind: '-', text: before[i]})
	}
	for ; j < cols; j++ {
		ops = append(ops, op{kind: '+', text: after[j]})
	}
	return ops
}

// hunks renders the edit script, showing only the changes and the few lines
// around them.
func hunks(ops []op) []string {
	interesting := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		for j := max(0, i-diffContext); j <= min(len(ops)-1, i+diffContext); j++ {
			interesting[j] = true
		}
	}
	var out []string
	oldLine, newLine := 1, 1
	for i := 0; i < len(ops); {
		if !interesting[i] {
			if ops[i].kind != '+' {
				oldLine++
			}
			if ops[i].kind != '-' {
				newLine++
			}
			i++
			continue
		}
		start, oldStart, newStart := i, oldLine, newLine
		var oldCount, newCount int
		for i < len(ops) && interesting[i] {
			if ops[i].kind != '+' {
				oldLine, oldCount = oldLine+1, oldCount+1
			}
			if ops[i].kind != '-' {
				newLine, newCount = newLine+1, newCount+1
			}
			i++
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount))
		for _, o := range ops[start:i] {
			out = append(out, string(o.kind)+o.text)
		}
	}
	return out
}
