// Package mutate writes the deliberate defects `uzushio task doctor` measures
// a verifier with.
//
// It parses Go with go/parser to find the sites, and then changes the source
// by splicing bytes at token.Position.Offset rather than by rewriting the tree
// and reprinting it. That is the whole design, and it is a decision rather than
// a shortcut: go/printer normalises the entire file — alignment, blank lines,
// and above all the placement of free-floating comments, which go/ast is
// documented as not moving for you — so a one-token mutant reprinted through
// it produces a diff of hundreds of lines on any file that was not already
// exactly gofmt's idea of itself. A splice produces one changed line, which is
// what a committed mutant has to be for a reviewer to read it. It is also
// immune to syntax the printer is older than.
//
// The operators are the small, well-tested set every mutation-testing tool
// starts from: the arithmetic swap, the relational boundary shift, the
// conditional negation, the constant bump, the removed statement and the
// zeroed return. A mapping that is guaranteed to produce an equivalent mutant
// is left out rather than generated and explained.
//
// Each operator also refuses the sites where the mutant provably would not
// compile — an array length bumped, a short variable declaration removed, a
// return zeroed to a type nobody can name. That is not tidiness: a mutant the
// toolchain refuses fails `go test ./...` whatever the tests do, so a verifier
// that did nothing but build the code would be scored as killing it. Generate
// then compiles what is left and drops whatever the syntactic rules missed.
package mutate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Operator names one way of breaking a program.
type Operator string

// The operators.
const (
	// OpArith swaps an arithmetic operator: + for -, * for /.
	OpArith Operator = "arith"
	// OpCond negates an `if` condition.
	OpCond Operator = "cond"
	// OpBound shifts a relational boundary: < for <=, > for >=, == for !=.
	OpBound Operator = "bound"
	// OpConst adds one to an integer literal.
	OpConst Operator = "const"
	// OpStmt removes one statement.
	OpStmt Operator = "stmt"
	// OpRet replaces a returned expression with the zero value of its type.
	OpRet Operator = "ret"
)

// String returns the operator as a file name and a manifest entry write it.
func (o Operator) String() string { return string(o) }

// AllOperators returns the operators, in the order a site is offered to them.
// The order is fixed rather than alphabetical so that two sites at one offset
// are always named in the same sequence.
func AllOperators() []Operator {
	return []Operator{OpArith, OpCond, OpBound, OpConst, OpStmt, OpRet}
}

// ParseOperator returns the operator a name spells.
func ParseOperator(name string) (Operator, error) {
	op := Operator(name)
	if slices.Contains(AllOperators(), op) {
		return op, nil
	}
	return "", fmt.Errorf("mutate: %q is not an operator (want one of %s)",
		name, strings.Join(OperatorNames(AllOperators()), ", "))
}

// OperatorNames renders operators as the plain strings a flag and a manifest
// entry both take.
func OperatorNames(ops []Operator) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.String())
	}
	return out
}

// Mutation is one site rewritten: where it was, what changed, and the file as
// it reads afterwards.
//
// Column is token.Position.Column, which is one-based and counted in bytes
// rather than runes. It is an identity rather than a measurement, and it is in
// the mutant's file name, so what matters is that it is the same on every
// machine.
type Mutation struct {
	Operator Operator
	// File is the path the source was read from, as the task names it.
	File string
	Line int
	// Column is one-based and counted in bytes.
	Column int
	// Note describes the change in one line, for the manifest entry.
	Note string
	// Source is the whole file with the splice applied.
	Source []byte
}

// generatedFile is the line a generated file carries, as the Go project
// defines it. A mutant of a generated file is a mutant of whatever generated
// it, which is not the task's code.
var generatedFile = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// Skip reports whether a file is one this package refuses to mutate, and why.
// A test file is not the subject: mutating the test the verifier runs measures
// nothing about the verifier.
func Skip(name string, src []byte) (string, bool) {
	switch {
	case !strings.HasSuffix(name, ".go"):
		return "not Go", true
	case strings.HasSuffix(name, "_test.go"):
		return "a test file", true
	case generatedFile.Match(src):
		return "generated", true
	}
	return "", false
}

// splice is one byte range replaced by one string. It is the only kind of edit
// this package makes.
type splice struct {
	operator Operator
	offset   int
	length   int
	text     string
	position token.Position
	note     string
}

// Mutations returns every mutation the given operators find in one file, in a
// deterministic order: by offset, and within one offset by the order
// AllOperators declares.
//
// The source is parsed once and every site is recorded against the original
// offsets, which stay valid because no splice is ever applied to the buffer the
// offsets were taken from — each mutation is a fresh copy of the original with
// exactly one range replaced.
func Mutations(name string, src []byte, ops []Operator) ([]Mutation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("mutate: parse %s: %w", name, err)
	}
	sites := collect(fset, file, src, ops)
	slices.SortStableFunc(sites, func(a, b splice) int {
		if a.offset != b.offset {
			return a.offset - b.offset
		}
		return slices.Index(AllOperators(), a.operator) - slices.Index(AllOperators(), b.operator)
	})

	out := make([]Mutation, 0, len(sites))
	for _, site := range sites {
		mutated := make([]byte, 0, len(src)-site.length+len(site.text))
		mutated = append(mutated, src[:site.offset]...)
		mutated = append(mutated, site.text...)
		mutated = append(mutated, src[site.offset+site.length:]...)
		if string(mutated) == string(src) {
			// A rewrite that changed nothing is an equivalent mutant by
			// construction — `return 0` zeroed, say. It is dropped here rather
			// than generated and then explained in the manifest.
			continue
		}
		out = append(out, Mutation{
			Operator: site.operator,
			File:     name,
			Line:     site.position.Line,
			Column:   site.position.Column,
			Note: fmt.Sprintf("%s:%d:%d: %s",
				name, site.position.Line, site.position.Column, site.note),
			Source: mutated,
		})
	}
	return out, nil
}

// collect walks the file once and records every site the enabled operators
// answer to.
func collect(fset *token.FileSet, file *ast.File, src []byte, ops []Operator) []splice {
	enabled := func(op Operator) bool { return slices.Contains(ops, op) }
	var sites []splice
	var stack []ast.Node
	add := func(site splice, pos token.Pos) {
		site.offset = fset.Position(pos).Offset
		site.position = fset.Position(pos)
		sites = append(sites, site)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		// Inspect calls back with nil after a node's children, but only where
		// the callback returned true for the node — so always returning true is
		// what makes this stack balance.
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if enabled(OpArith) {
				if to, ok := arithSwap(node); ok {
					add(splice{
						operator: OpArith, length: len(node.Op.String()), text: to,
						note: fmt.Sprintf("%q -> %q", node.Op.String(), to),
					}, node.OpPos)
				}
			}
			if enabled(OpBound) {
				if to, ok := boundShift(node.Op); ok {
					add(splice{
						operator: OpBound, length: len(node.Op.String()), text: to,
						note: fmt.Sprintf("%q -> %q", node.Op.String(), to),
					}, node.OpPos)
				}
			}
		case *ast.IfStmt:
			if enabled(OpCond) && node.Cond != nil {
				start := fset.Position(node.Cond.Pos()).Offset
				end := fset.Position(node.Cond.End()).Offset
				add(splice{
					operator: OpCond, length: end - start,
					text: "!(" + string(src[start:end]) + ")",
					note: "negate the if condition",
				}, node.Cond.Pos())
			}
		case *ast.BasicLit:
			if enabled(OpConst) && node.Kind == token.INT && mutableInt(stack) {
				if to, ok := bump(node.Value); ok {
					add(splice{
						operator: OpConst, length: len(node.Value), text: to,
						note: fmt.Sprintf("%s -> %s", node.Value, to),
					}, node.Pos())
				}
			}
		case *ast.BlockStmt:
			if enabled(OpStmt) {
				for _, stmt := range node.List {
					if site, ok := removal(fset, src, stmt); ok {
						sites = append(sites, site)
					}
				}
			}
		case *ast.ReturnStmt:
			if enabled(OpRet) && len(node.Results) == 1 {
				if to, ok := zeroReturn(stack, node.Results[0]); ok {
					start := fset.Position(node.Results[0].Pos()).Offset
					end := fset.Position(node.Results[0].End()).Offset
					add(splice{
						operator: OpRet, length: end - start, text: to,
						note: fmt.Sprintf("return %s -> return %s", string(src[start:end]), to),
					}, node.Results[0].Pos())
				}
			}
		}
		return true
	})
	return sites
}

// arithMap is the arithmetic swap, written as a table rather than a switch:
// the token type has a hundred members and a switch over six of them is a
// switch the reader has to be told is deliberate.
var arithMap = map[token.Token]token.Token{
	token.ADD: token.SUB,
	token.SUB: token.ADD,
	token.MUL: token.QUO,
	token.QUO: token.MUL,
}

// arithSwap returns the operator an arithmetic one is swapped for.
//
// It refuses `+` where either side looks like a string, because `"a" - "b"`
// does not compile and a mutant that does not compile teaches nothing about
// the verifier — it is killed by the toolchain rather than by the test. The
// judgement is syntactic and therefore best effort: a string literal, a
// concatenation of one, a parenthesised one, or a string(...) conversion are
// recognised; a string held in a variable is not.
func arithSwap(node *ast.BinaryExpr) (string, bool) {
	to, ok := arithMap[node.Op]
	if !ok {
		return "", false
	}
	if node.Op == token.ADD && (stringish(node.X) || stringish(node.Y)) {
		return "", false
	}
	return to.String(), true
}

// stringish reports whether an expression is obviously a string.
func stringish(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING
	case *ast.ParenExpr:
		return stringish(e.X)
	case *ast.BinaryExpr:
		return e.Op == token.ADD && (stringish(e.X) || stringish(e.Y))
	case *ast.CallExpr:
		ident, ok := e.Fun.(*ast.Ident)
		return ok && ident.Name == "string"
	}
	return false
}

// boundMap is the relational boundary shift. The three pairs are their own
// inverses, so the mutant of a mutant is the original — which is what makes
// the set closed and the naming stable.
var boundMap = map[token.Token]token.Token{
	token.LSS: token.LEQ,
	token.LEQ: token.LSS,
	token.GTR: token.GEQ,
	token.GEQ: token.GTR,
	token.EQL: token.NEQ,
	token.NEQ: token.EQL,
}

// boundShift returns the relational operator a boundary is shifted to.
func boundShift(op token.Token) (string, bool) {
	to, ok := boundMap[op]
	if !ok {
		return "", false
	}
	return to.String(), true
}

// mutableInt reports whether an integer literal is one worth bumping.
//
// Two positions are refused because the mutant cannot compile, and a mutant the
// compiler kills measures the compiler rather than the tests.
//
//  1. An array length. `[3]int` becomes `[4]int` and every value of the old
//     type stops assigning to the new one.
//  2. A literal inside a const block that uses iota. The block's specs are
//     tied together — a later spec repeats the first one's expression — so
//     changing one literal changes the type or the arithmetic of the rest.
//
// The stack's last element is the literal itself, so the element before it is
// its parent.
func mutableInt(stack []ast.Node) bool {
	if len(stack) >= 2 {
		if array, ok := stack[len(stack)-2].(*ast.ArrayType); ok && array.Len == stack[len(stack)-1] {
			return false
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		decl, ok := stack[i].(*ast.GenDecl)
		if !ok {
			continue
		}
		return decl.Tok != token.CONST || !usesIota(decl)
	}
	return true
}

// usesIota reports whether a declaration mentions iota anywhere.
func usesIota(decl *ast.GenDecl) bool {
	found := false
	ast.Inspect(decl, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == "iota" {
			found = true
		}
		return !found
	})
	return found
}

// bump adds one to an integer literal, keeping the answer in decimal. A
// literal Go itself will not read — one that overflows an int64 — is left
// alone rather than guessed at.
func bump(literal string) (string, bool) {
	clean := strings.ReplaceAll(literal, "_", "")
	value, err := strconv.ParseInt(clean, 0, 64)
	if err != nil {
		return "", false
	}
	if value == 1<<63-1 {
		return "", false
	}
	return strconv.FormatInt(value+1, 10), true
}

// removal deletes one statement, whole lines and all.
//
// Only an assignment and an expression statement are candidates: removing a
// declaration or a return changes what the function is rather than what it
// does, and both usually stop it compiling. A short variable declaration is
// refused for the same reason spelled the other way round — `x := f()` is a
// declaration wearing an assignment's syntax, and removing it leaves every
// later use of x undefined, so the mutant does not compile and any verifier
// that builds the code scores it killed for nothing.
//
// The statement has to own its lines — nothing but whitespace before it,
// nothing but whitespace or a trailing comment after it — so that the deletion
// is a run of whole lines and the diff has no partial line in it.
func removal(fset *token.FileSet, src []byte, stmt ast.Stmt) (splice, bool) {
	switch node := stmt.(type) {
	case *ast.AssignStmt:
		if node.Tok == token.DEFINE {
			return splice{}, false
		}
	case *ast.ExprStmt:
	default:
		return splice{}, false
	}
	position := fset.Position(stmt.Pos())
	start, end := position.Offset, fset.Position(stmt.End()).Offset

	lineStart := start
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	if strings.TrimSpace(string(src[lineStart:start])) != "" {
		return splice{}, false
	}
	lineEnd := end
	for lineEnd < len(src) && src[lineEnd] != '\n' {
		lineEnd++
	}
	rest := strings.TrimSpace(string(src[end:lineEnd]))
	if rest != "" && !strings.HasPrefix(rest, "//") {
		return splice{}, false
	}
	if lineEnd < len(src) {
		lineEnd++ // the newline goes with the line
	}
	return splice{
		operator: OpStmt,
		offset:   lineStart,
		length:   lineEnd - lineStart,
		position: position,
		note:     "remove the statement",
	}, true
}

// zeroReturn returns the zero value a single returned expression is replaced
// with, and whether the type is obvious enough to say.
//
// The declared result type is the only thing read: a function declared to
// return an int returns 0 whatever the expression looks like. Two cases are
// skipped rather than guessed at — a function with more than one result, where
// which result to zero is a choice this operator does not make, and a result
// type this package cannot resolve, where a guess from the expression's shape
// buys a handful of mutants and a steady supply of ones that do not compile.
func zeroReturn(stack []ast.Node, _ ast.Expr) (string, bool) {
	results, ok := singleResultType(stack)
	if !ok {
		return "", false
	}
	return zeroOf(results)
}

// singleResultType returns the declared type of the enclosing function's one
// result, where it has exactly one.
func singleResultType(stack []ast.Node) (ast.Expr, bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		var results *ast.FieldList
		switch node := stack[i].(type) {
		case *ast.FuncDecl:
			results = node.Type.Results
		case *ast.FuncLit:
			results = node.Type.Results
		default:
			continue
		}
		if results == nil || len(results.List) != 1 || len(results.List[0].Names) > 1 {
			return nil, false
		}
		return results.List[0].Type, true
	}
	return nil, false
}

// zeroOf returns the zero value of a type written out in source, for the types
// whose zero value can be read off the syntax alone.
func zeroOf(t ast.Expr) (string, bool) {
	switch t := t.(type) {
	case *ast.Ident:
		switch t.Name {
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
			"byte", "rune", "float32", "float64", "complex64", "complex128":
			return "0", true
		case "string":
			return `""`, true
		case "bool":
			return "false", true
		case "error", "any":
			return "nil", true
		}
	case *ast.StarExpr, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType:
		return "nil", true
	case *ast.ArrayType:
		// A slice's zero value is nil; an array's is not, and there is no way
		// to write it in one token.
		if t.Len == nil {
			return "nil", true
		}
	}
	return "", false
}
