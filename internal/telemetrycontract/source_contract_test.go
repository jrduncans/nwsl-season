package telemetrycontract_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	telemetryImport = "github.com/jrduncans/nwsl-season/internal/telemetry"
	nwslconvImport  = "github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
)

// TestProductionTelemetryUsesGeneratedConventions keeps every production
// signal and error code mechanically connected to the Weaver registry. The
// generated-artifact check then proves those constants came from the registry.
func TestProductionTelemetryUsesGeneratedConventions(t *testing.T) {
	repositoryRoot := sourceRepositoryRoot(t)
	var findings []string
	for _, sourceRoot := range []string{"cmd", "internal"} {
		root := filepath.Join(repositoryRoot, sourceRoot)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if filepath.ToSlash(relative) == "internal/telemetry/nwslconv" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fileFindings, err := inspectTelemetrySource(path, relative)
			if err != nil {
				return err
			}
			findings = append(findings, fileFindings...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	sort.Strings(findings)
	if len(findings) != 0 {
		t.Fatalf("production telemetry bypasses generated Weaver conventions:\n%s", strings.Join(findings, "\n"))
	}
}

func sourceRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func inspectTelemetrySource(path, relative string) ([]string, error) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relative, err)
	}
	telemetryAlias := importedAlias(parsed, telemetryImport)
	nwslconvAlias := importedAlias(parsed, nwslconvImport)
	telemetryTracers := assignedTelemetryTracers(parsed, telemetryAlias)
	var findings []string
	addFinding := func(call *ast.CallExpr, message string) {
		position := files.Position(call.Pos())
		findings = append(findings, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(relative), position.Line, message))
	}

	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if selector.Sel.Name == "Start" && isTelemetryTracer(selector.X, telemetryAlias, telemetryTracers) {
			if len(call.Args) < 2 || !isGeneratedSelector(call.Args[1], nwslconvAlias, "Span") {
				addFinding(call, "telemetry.Tracer().Start must use a generated nwslconv.Span* name")
			}
		}
		if selector.Sel.Name == "AddEvent" {
			if len(call.Args) < 1 || !isGeneratedSelector(call.Args[0], nwslconvAlias, "Event") {
				addFinding(call, "trace Span.AddEvent must use a generated nwslconv.Event* name")
			}
		}

		packageName, functionName, ok := qualifiedSelector(call.Fun)
		if !ok || packageName != telemetryAlias {
			return true
		}
		switch functionName {
		case "RecordCompletedSpan", "RecordCompletedWarningSpan":
			if len(call.Args) < 2 || !isGeneratedSelector(call.Args[1], nwslconvAlias, "Span") {
				addFinding(call, "completed telemetry spans must use a generated nwslconv.Span* name")
			}
			if len(call.Args) < 7 || (!isEmptyString(call.Args[6]) && !isGeneratedSelector(call.Args[6], nwslconvAlias, "ErrorCode")) {
				addFinding(call, "completed telemetry span error codes must be empty or a generated nwslconv.ErrorCode* value")
			}
		case "RecordErrorWithCode", "RecordErrorWithType", "RecordWarningWithType":
			if len(call.Args) < 4 || (!isEmptyString(call.Args[3]) && !isGeneratedSelector(call.Args[3], nwslconvAlias, "ErrorCode")) {
				addFinding(call, "telemetry error codes must be empty or a generated nwslconv.ErrorCode* value")
			}
		}
		return true
	})
	return findings, nil
}

func TestInspectTelemetrySourceRejectsRawConventions(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		findingCount int
	}{
		{
			name: "generated",
			source: `package sample
import (
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
)
func record(ctx, span, err any) {
	telemetry.Tracer().Start(ctx, nwslconv.SpanExample)
	span.AddEvent(nwslconv.EventExample)
	telemetry.RecordWarningWithType(ctx, span, err, nwslconv.ErrorCodeExample, "failure")
}`,
		},
		{
			name: "raw direct and assigned",
			source: `package sample
import "github.com/jrduncans/nwsl-season/internal/telemetry"
func record(ctx, span, err any) {
	telemetry.Tracer().Start(
		ctx,
		"raw.direct",
	)
	tracer := telemetry.Tracer()
	tracer.Start(ctx, "raw.assigned")
	span.AddEvent("raw.event")
	telemetry.RecordWarningWithType(ctx, span, err, "raw.code", "failure")
}`,
			findingCount: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sample.go")
			if err := os.WriteFile(path, []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			findings, err := inspectTelemetrySource(path, "sample.go")
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != test.findingCount {
				t.Fatalf("findings = %v, want %d", findings, test.findingCount)
			}
		})
	}
}

func importedAlias(file *ast.File, importPath string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		parts := strings.Split(path, "/")
		return parts[len(parts)-1]
	}
	return ""
}

func assignedTelemetryTracers(file *ast.File, telemetryAlias string) map[string]bool {
	tracers := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for index, value := range statement.Rhs {
				if index >= len(statement.Lhs) || !isTelemetryTracerCall(value, telemetryAlias) {
					continue
				}
				if identifier, ok := statement.Lhs[index].(*ast.Ident); ok {
					tracers[identifier.Name] = true
				}
			}
		case *ast.ValueSpec:
			for index, value := range statement.Values {
				if index >= len(statement.Names) || !isTelemetryTracerCall(value, telemetryAlias) {
					continue
				}
				tracers[statement.Names[index].Name] = true
			}
		}
		return true
	})
	return tracers
}

func isTelemetryTracer(expression ast.Expr, telemetryAlias string, assigned map[string]bool) bool {
	if isTelemetryTracerCall(expression, telemetryAlias) {
		return true
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && assigned[identifier.Name]
}

func isTelemetryTracerCall(expression ast.Expr, telemetryAlias string) bool {
	if telemetryAlias == "" {
		return false
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	packageName, functionName, ok := qualifiedSelector(call.Fun)
	return ok && packageName == telemetryAlias && functionName == "Tracer"
}

func qualifiedSelector(expression ast.Expr) (string, string, bool) {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return identifier.Name, selector.Sel.Name, true
}

func isGeneratedSelector(expression ast.Expr, nwslconvAlias, prefix string) bool {
	packageName, name, ok := qualifiedSelector(expression)
	return ok && nwslconvAlias != "" && packageName == nwslconvAlias && strings.HasPrefix(name, prefix)
}

func isEmptyString(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.STRING && literal.Value == `""`
}
