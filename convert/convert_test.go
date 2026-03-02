package convert

import (
	"encoding/json"
	"strings"
	"testing"
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
