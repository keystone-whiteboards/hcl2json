package convert

import (
	"encoding/json"
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func TestSingleBlockNewFormat(t *testing.T) {
	root := convertSingle(t, `
block "label_one" {
	attribute = "value"
}`)

	if root["$type"] != "root" {
		t.Fatalf("expected root type, got %v", root["$type"])
	}

	blocks := asMap(t, root["blocks"])
	blockList := asSlice(t, blocks["block"])
	if len(blockList) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blockList))
	}

	block := asMap(t, blockList[0])
	if block["$type"] != "block" {
		t.Fatalf("expected block type node, got %v", block["$type"])
	}

	labels := asSlice(t, block["labels"])
	if len(labels) != 1 || labels[0] != "label_one" {
		t.Fatalf("unexpected labels: %#v", labels)
	}

	attrs := asMap(t, block["attributes"])
	attr := asMap(t, attrs["attribute"])
	if attr["$type"] != "literal" {
		t.Fatalf("expected literal attribute, got %v", attr["$type"])
	}
	if attr["value"] != "value" {
		t.Fatalf("expected literal value 'value', got %v", attr["value"])
	}
	assertHasRange(t, attr, "test.hcl")
	if _, ok := attr["$exprType"]; !ok {
		t.Fatal("expected $exprType field on expression node")
	}
}

func TestNestedBlockAndLabelsNewFormat(t *testing.T) {
	root := convertSingle(t, `
block "label_one" "label_two" {
	nested_block {}
}`)

	blocks := asMap(t, root["blocks"])
	block := asMap(t, asSlice(t, blocks["block"])[0])
	labels := asSlice(t, block["labels"])
	if len(labels) != 2 || labels[0] != "label_one" || labels[1] != "label_two" {
		t.Fatalf("unexpected labels: %#v", labels)
	}

	nested := asMap(t, block["blocks"])
	nestedList := asSlice(t, nested["nested_block"])
	if len(nestedList) != 1 {
		t.Fatalf("expected one nested_block, got %d", len(nestedList))
	}
}

func TestMultipleBlocksAreNotMerged(t *testing.T) {
	root := convertSingle(t, `
block "label_one" { attribute = "value" }
block "label_one" { attribute = "value_two" }
`)

	blocks := asMap(t, root["blocks"])
	blockList := asSlice(t, blocks["block"])
	if len(blockList) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blockList))
	}

	first := asMap(t, asMap(t, blockList[0])["attributes"])
	second := asMap(t, asMap(t, blockList[1])["attributes"])

	if asMap(t, first["attribute"])["value"] != "value" {
		t.Fatalf("unexpected first block value: %v", asMap(t, first["attribute"])["value"])
	}
	if asMap(t, second["attribute"])["value"] != "value_two" {
		t.Fatalf("unexpected second block value: %v", asMap(t, second["attribute"])["value"])
	}
}

func TestExpressionNodeMetadata(t *testing.T) {
	root := convertSingle(t, `
a = 1 + 2
b = [1, 2, 3]
c = foo.bar
`)

	attrs := asMap(t, root["attributes"])

	a := asMap(t, attrs["a"])
	if a["$type"] != "binary" {
		t.Fatalf("expected binary expression for a, got %v", a["$type"])
	}
	assertHasRange(t, a, "test.hcl")

	b := asMap(t, attrs["b"])
	if b["$type"] != "tuple" {
		t.Fatalf("expected tuple expression for b, got %v", b["$type"])
	}

	c := asMap(t, attrs["c"])
	if c["$type"] != "scope-traversal" {
		t.Fatalf("expected scope-traversal expression for c, got %v", c["$type"])
	}
	parts := asSlice(t, c["parts"])
	if len(parts) < 2 {
		t.Fatalf("expected traversal parts, got %#v", parts)
	}
}

func TestFilesMergeCombinedOutput(t *testing.T) {
	root := convertMulti(t,
		InputFile{Bytes: []byte(`
block "one" {
	x = 1
}
a = 1
`), Filename: "one.hcl"},
		InputFile{Bytes: []byte(`
block "two" {
	y = 2
}
b = 2
`), Filename: "two.hcl"},
	)

	if root["$type"] != "root" {
		t.Fatalf("expected root type, got %v", root["$type"])
	}

	attrs := asMap(t, root["attributes"])
	if _, ok := attrs["a"]; !ok {
		t.Fatal("expected attribute a from first file")
	}
	if _, ok := attrs["b"]; !ok {
		t.Fatal("expected attribute b from second file")
	}

	blocks := asMap(t, root["blocks"])
	blockList := asSlice(t, blocks["block"])
	if len(blockList) != 2 {
		t.Fatalf("expected 2 merged block entries, got %d", len(blockList))
	}
}

func TestFilesDuplicateTopLevelAttributeError(t *testing.T) {
	_, err := Files([]InputFile{
		{Bytes: []byte(`a = 1`), Filename: "one.hcl"},
		{Bytes: []byte(`a = 2`), Filename: "two.hcl"},
	}, Options{})
	if err == nil {
		t.Fatal("expected duplicate attribute error")
	}
	if !strings.Contains(err.Error(), "duplicate top-level attribute across files: a") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTemplateEscapesNewFormat(t *testing.T) {
	root := convertSingle(t, `
v = "$${one}"
x = "${var.y} $${oh}"
y = "%{ if true }%%{hi}%{ endif }"
z = "%%{oh"
`)

	attrs := asMap(t, root["attributes"])
	for _, key := range []string{"v", "z"} {
		node := asMap(t, attrs[key])
		if node["$type"] != "literal" {
			t.Fatalf("expected %s to be literal node, got %v", key, node["$type"])
		}
		assertHasRange(t, node, "test.hcl")
	}

	for _, key := range []string{"x", "y"} {
		node := asMap(t, attrs[key])
		if node["$type"] != "template" {
			t.Fatalf("expected %s to be template node, got %v", key, node["$type"])
		}
		assertHasRange(t, node, "test.hcl")
	}
}

func convertSingle(t *testing.T, input string) map[string]interface{} {
	t.Helper()
	return convertMulti(t, InputFile{Bytes: []byte(input), Filename: "test.hcl"})
}

func convertMulti(t *testing.T, files ...InputFile) map[string]interface{} {
	t.Helper()
	converted, err := Files(files, Options{})
	if err != nil {
		t.Fatalf("convert files: %v", err)
	}

	var root map[string]interface{}
	if err := json.Unmarshal(converted, &root); err != nil {
		t.Fatalf("unmarshal converted output: %v", err)
	}
	return root
}

func assertHasRange(t *testing.T, node map[string]interface{}, expectedFilename string) {
	t.Helper()
	rangeValue, ok := node["$range"]
	if !ok {
		t.Fatalf("missing $range in node: %#v", node)
	}
	rangeMap := asMap(t, rangeValue)
	if rangeMap["filename"] != expectedFilename {
		t.Fatalf("expected range filename %q, got %v", expectedFilename, rangeMap["filename"])
	}
	if _, ok := rangeMap["start"]; !ok {
		t.Fatalf("missing range start: %#v", rangeMap)
	}
	if _, ok := rangeMap["end"]; !ok {
		t.Fatalf("missing range end: %#v", rangeMap)
	}
}

func asMap(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	m, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T (%#v)", value, value)
	}
	return m
}

func asSlice(t *testing.T, value interface{}) []interface{} {
	t.Helper()
	s, ok := value.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T (%#v)", value, value)
	}
	return s
}

func TestExpressionSourceByNodeType(t *testing.T) {
	t.Run("literal", func(t *testing.T) {
		node := convertExpr(t, "1")
		assertExprNodeSource(t, node, "literal", "1")
	})

	t.Run("tuple", func(t *testing.T) {
		node := convertExpr(t, "[1, 2]")
		assertExprNodeSource(t, node, "tuple", "[1, 2]")
	})

	t.Run("object", func(t *testing.T) {
		node := convertExpr(t, "{ a = 1 }")
		assertExprNodeSource(t, node, "object", "{a = 1}")
	})

	t.Run("unary", func(t *testing.T) {
		node := convertExpr(t, "-1")
		assertExprNodeSource(t, node, "unary", "-1")
	})

	t.Run("binary", func(t *testing.T) {
		node := convertExpr(t, "1 + 1")
		assertExprNodeSource(t, node, "binary", "1 + 1")
	})

	t.Run("function", func(t *testing.T) {
		node := convertExpr(t, "max(1, 2)")
		assertExprNodeSource(t, node, "function", "max(1, 2)")
	})

	t.Run("template", func(t *testing.T) {
		node := convertExpr(t, `"hello ${1}"`)
		assertExprNodeSource(t, node, "template", `"hello ${1}"`)
	})

	t.Run("template-join", func(t *testing.T) {
		node := convertExpr(t, `"%{for x in [1, 2]}${x}%{endfor}"`)
		assertExprNodeSource(t, node, "template", `"%{for x in [1, 2]}${x}%{endfor}"`)

		parts := asSlice(t, node["parts"])
		if len(parts) == 0 {
			t.Fatal("expected template parts")
		}

		join := asMap(t, parts[0])
		assertExprNodeSource(t, join, "template-join", "%{for x in [1, 2]}${x}%{endfor}")
	})

	t.Run("conditional", func(t *testing.T) {
		node := convertExpr(t, "true ? 1 : 0")
		assertExprNodeSource(t, node, "conditional", "true ? 1 : 0")
	})

	t.Run("for", func(t *testing.T) {
		node := convertExpr(t, "[for x in [1, 2] : x]")
		assertExprNodeSource(t, node, "for", "[for x in [1, 2] : x]")
	})

	t.Run("for-source-tuple-no-condition", func(t *testing.T) {
		node := convertExpr(t, "[for x in [1, 2] : x]")
		assertExprNodeSource(t, node, "for", "[for x in [1, 2] : x]")
	})

	t.Run("for-source-tuple-with-condition", func(t *testing.T) {
		node := convertExpr(t, "[for x in [1, 2] : x if x > 1]")
		assertExprNodeSource(t, node, "for", "[for x in [1, 2] : x if x > 1]")
	})

	t.Run("for-source-object-no-condition", func(t *testing.T) {
		node := convertExpr(t, "{for k, v in {a = 1, b = 2} : k => v}")
		assertExprNodeSource(t, node, "for", "{for k, v in {a = 1, b = 2} : k => v}")
	})

	t.Run("for-source-object-with-condition", func(t *testing.T) {
		node := convertExpr(t, "{for k, v in {a = 1, b = 2} : k => v if v > 1}")
		assertExprNodeSource(t, node, "for", "{for k, v in {a = 1, b = 2} : k => v if v > 1}")
	})

	t.Run("index", func(t *testing.T) {
		node := convertExpr(t, "var.list[local.i]")
		assertExprNodeSource(t, node, "index", "var.list[local.i]")
	})

	t.Run("scope-traversal", func(t *testing.T) {
		node := convertExpr(t, "var.foo")
		assertExprNodeSource(t, node, "scope-traversal", "var.foo")
	})

	t.Run("relative-traversal", func(t *testing.T) {
		node := convertExpr(t, "(var.foo).bar")
		assertExprNodeSource(t, node, "relative-traversal", "(var.foo).bar")
	})

	t.Run("parentheses", func(t *testing.T) {
		node := convertExpr(t, "(1 + 2)")
		assertExprNodeSource(t, node, "parentheses", "(1 + 2)")
	})
}

func convertExpr(t *testing.T, exprSource string) map[string]interface{} {
	t.Helper()

	expr, diags := hclsyntax.ParseExpression([]byte(exprSource), "expr.hcl", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatalf("parse expression %q: %v", exprSource, diags.Errs())
	}

	c := converter{bytes: []byte(exprSource), options: Options{}}
	node, err := c.ConvertExpression(expr)
	if err != nil {
		t.Fatalf("convert expression %q: %v", exprSource, err)
	}

	return node
}

func assertExprNodeSource(t *testing.T, node map[string]interface{}, expectedType, expectedSource string) {
	t.Helper()

	if node["$type"] != expectedType {
		t.Fatalf("expected node type %q, got %v", expectedType, node["$type"])
	}

	source, ok := node["$source"]
	if !ok {
		t.Fatalf("expected $source on %q node", expectedType)
	}

	if source != expectedSource {
		t.Fatalf("expected $source %q, got %v", expectedSource, source)
	}
}
