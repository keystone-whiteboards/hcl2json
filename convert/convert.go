package convert

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

type Options struct {
}

type InputFile struct {
	Bytes    []byte
	Filename string
}

// Files takes the contents of one or more HCL files, as bytes, and converts
// them into a single JSON representation. Blocks of the same type are appended,
// and top-level attributes are merged.
func Files(files []InputFile, options Options) ([]byte, error) {
	convertedFiles := make([]jsonObj, 0, len(files))
	for _, inputFile := range files {
		file, diags := hclsyntax.ParseConfig(inputFile.Bytes, inputFile.Filename, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse config %s: %v", inputFile.Filename, diags.Errs())
		}

		convertedFile, err := ConvertFile(file, options)
		if err != nil {
			return nil, fmt.Errorf("convert file %s: %w", inputFile.Filename, err)
		}

		convertedFiles = append(convertedFiles, convertedFile)
	}

	merged, err := mergeConvertedFiles(convertedFiles)
	if err != nil {
		return nil, fmt.Errorf("merge converted files: %w", err)
	}

	jsonBytes, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}

	return jsonBytes, nil
}

func mergeConvertedFiles(files []jsonObj) (jsonObj, error) {
	merged := makeNode("root", map[string]interface{}{
		"blocks":     make(jsonObj),
		"attributes": make(jsonObj),
	})

	mergedBlocks := merged["blocks"].(jsonObj)
	mergedAttributes := merged["attributes"].(jsonObj)

	for _, convertedFile := range files {
		blocks, ok := convertedFile["blocks"].(jsonObj)
		if !ok {
			return nil, fmt.Errorf("converted file missing blocks")
		}

		attributes, ok := convertedFile["attributes"].(jsonObj)
		if !ok {
			return nil, fmt.Errorf("converted file missing attributes")
		}

		for blockType, blocksForType := range blocks {
			typedBlocks, ok := blocksForType.([]interface{})
			if !ok {
				return nil, fmt.Errorf("blocks for type %s are not a list", blockType)
			}
			if _, exists := mergedBlocks[blockType]; !exists {
				mergedBlocks[blockType] = []interface{}{}
			}
			mergedBlocks[blockType] = append(mergedBlocks[blockType].([]interface{}), typedBlocks...)
		}

		for attrName, attrValue := range attributes {
			if _, exists := mergedAttributes[attrName]; exists {
				return nil, fmt.Errorf("duplicate top-level attribute across files: %s", attrName)
			}
			mergedAttributes[attrName] = attrValue
		}
	}

	return merged, nil
}

// File takes an HCL file and converts it to its JSON representation.
func File(file *hcl.File, options Options) ([]byte, error) {
	convertedFile, err := ConvertFile(file, options)
	if err != nil {
		return nil, fmt.Errorf("convert file: %w", err)
	}

	jsonBytes, err := json.Marshal(convertedFile)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}

	return jsonBytes, nil
}

type jsonObj = map[string]interface{}

type converter struct {
	bytes   []byte
	options Options
}

func ConvertFile(file *hcl.File, options Options) (jsonObj, error) {
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("convert file body to body type")
	}

	c := converter{
		bytes:   file.Bytes,
		options: options,
	}

	n := makeNode("root", map[string]interface{}{})
	err := c.convertBody(body, n)
	if err != nil {
		return nil, fmt.Errorf("convert body: %w", err)
	}

	return n, nil
}

func makeNode(exprType string, value map[string]interface{}) jsonObj {
	n := make(jsonObj)
	n["$type"] = exprType
	for k, v := range value {
		n[k] = v
	}
	return n
}

func makeRange(r hcl.Range) map[string]interface{} {
	n := make(jsonObj)
	n["filename"] = r.Filename
	n["start"] = map[string]interface{}{
		"line":   r.Start.Line,
		"column": r.Start.Column,
		"byte":   r.Start.Byte,
	}
	n["end"] = map[string]interface{}{
		"line":   r.End.Line,
		"column": r.End.Column,
		"byte":   r.End.Byte,
	}
	return n
}

func (c *converter) makeExprNode(exprType string, expr hclsyntax.Expression, value map[string]interface{}, sourceBuilder func() string) jsonObj {
	n := make(jsonObj)
	n["$type"] = exprType
	n["$range"] = makeRange(expr.Range())
	v, _ := expr.Value(&evalContext)
	n["$exprType"] = v.Type().FriendlyName()
	if v.IsWhollyKnown() {
		n["$exprValue"] = v.GoString()
	} else {
		n["$exprValue"] = nil
	}
	if sourceBuilder != nil {
		n["$source"] = sourceBuilder()
	}
	for k, v := range value {
		n[k] = v
	}
	return n
}

func sourceFromNode(node jsonObj) string {
	source, _ := node["$source"].(string)
	return source
}

func (c *converter) literalSource(v cty.Value) string {
	if !v.IsKnown() {
		return "null"
	}

	if v.IsNull() {
		return "null"
	}

	if v.Type() == cty.String {
		return strconv.Quote(v.AsString())
	}

	if v.Type() == cty.Bool {
		if v.True() {
			return "true"
		}
		return "false"
	}

	if v.Type() == cty.Number {
		return v.AsBigFloat().Text('g', -1)
	}

	b, err := json.Marshal(ctyjson.SimpleJSONValue{Value: v})
	if err != nil {
		return v.GoString()
	}

	return string(b)
}

func (c *converter) traversalPartSource(part hcl.Traverser, first bool) string {
	switch p := part.(type) {
	case hcl.TraverseRoot:
		if first {
			return p.Name
		}
		return "." + p.Name
	case hcl.TraverseAttr:
		if first {
			return p.Name
		}
		return "." + p.Name
	case hcl.TraverseIndex:
		return "[" + c.literalSource(p.Key) + "]"
	default:
		return ""
	}
}

func (c *converter) rangeSource(r hcl.Range) string {
	// for some reason the range doesn't include the ending paren, so
	// check if the next character is an ending paren, and include it if it is.
	end := r.End.Byte
	if end < len(c.bytes) && c.bytes[end] == ')' {
		end++
	}
	return string(c.bytes[r.Start.Byte:end])
}

func (c *converter) convertBody(body *hclsyntax.Body, n jsonObj) error {
	blocks := make(jsonObj)
	for _, block := range body.Blocks {
		if err := c.convertBlock(block, blocks); err != nil {
			return fmt.Errorf("convert block: %w", err)
		}
	}

	attrs := make(jsonObj)
	var err error
	for key, value := range body.Attributes {
		attrs[key], err = c.ConvertExpression(value.Expr)
		if err != nil {
			return fmt.Errorf("convert expression: %w", err)
		}
	}

	n["blocks"] = blocks
	n["attributes"] = attrs

	return nil
}

func (c *converter) convertBlock(block *hclsyntax.Block, blocks jsonObj) error {
	if _, exists := blocks[block.Type]; !exists {
		blocks[block.Type] = []interface{}{}
	}

	n := makeNode("block", map[string]interface{}{
		"$range": makeRange(block.Range()),
		"labels": block.Labels,
	})

	err := c.convertBody(block.Body, n)
	if err != nil {
		return fmt.Errorf("convert body: %w", err)
	}

	blocks[block.Type] = append(blocks[block.Type].([]interface{}), n)

	return nil
}

func (c *converter) convertLiteralValueExpr(expr *hclsyntax.LiteralValueExpr) (jsonObj, error) {
	return c.makeExprNode("literal", expr, map[string]interface{}{
		"value": ctyjson.SimpleJSONValue{Value: expr.Val},
	}, func() string {
		return c.literalSource(expr.Val)
	}), nil
}

func (c *converter) convertTupleConsExpr(expr *hclsyntax.TupleConsExpr) (jsonObj, error) {
	var items []jsonObj
	for _, ex := range expr.Exprs {
		elem, err := c.ConvertExpression(ex)
		if err != nil {
			return nil, err
		}
		items = append(items, elem)
	}

	itemSources := make([]string, 0, len(items))
	for _, item := range items {
		itemSources = append(itemSources, sourceFromNode(item))
	}

	return c.makeExprNode("tuple", expr, map[string]interface{}{
		"items": items,
	}, func() string {
		return "[" + strings.Join(itemSources, ", ") + "]"
	}), nil
}

func (c *converter) convertObjectConsExpr(expr *hclsyntax.ObjectConsExpr) (jsonObj, error) {
	var props []jsonObj
	var propSources []string
	for _, item := range expr.Items {
		keyExpr := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
		wrappedExpr := keyExpr.Wrapped

		keySource := ""
		if _, isTraversal := wrappedExpr.(*hclsyntax.ScopeTraversalExpr); isTraversal && !keyExpr.ForceNonLiteral {
			keySource = strings.TrimSpace(c.rangeSource(wrappedExpr.Range()))
			wrappedExpr = &hclsyntax.LiteralValueExpr{Val: cty.StringVal(c.rangeSource(wrappedExpr.Range())), SrcRange: wrappedExpr.Range()}
		}

		key, err := c.ConvertExpression(wrappedExpr)
		if err != nil {
			return nil, err
		}

		value, err := c.ConvertExpression(item.ValueExpr)
		if err != nil {
			return nil, err
		}

		props = append(props, map[string]interface{}{
			"key":   key,
			"value": value,
		})

		if keySource == "" {
			keySource = sourceFromNode(key)
		}
		propSources = append(propSources, keySource+" = "+sourceFromNode(value))
	}

	return c.makeExprNode("object", expr, map[string]interface{}{
		"props": props,
	}, func() string {
		return "{" + strings.Join(propSources, ", ") + "}"
	}), nil
}

func (c *converter) convertUnaryOpExpr(expr *hclsyntax.UnaryOpExpr) (jsonObj, error) {
	arg, err := c.ConvertExpression(expr.Val)
	if err != nil {
		return nil, fmt.Errorf("convert unary arg: %w", err)
	}
	op := strings.TrimSpace(c.rangeSource(expr.SymbolRange))

	return c.makeExprNode("unary", expr, map[string]interface{}{
		"op":  c.rangeSource(expr.SymbolRange),
		"arg": arg,
	}, func() string {
		return op + sourceFromNode(arg)
	}), nil
}

func (c *converter) convertBinaryOpExpr(expr *hclsyntax.BinaryOpExpr) (jsonObj, error) {
	left, err := c.ConvertExpression(expr.LHS)
	if err != nil {
		return nil, fmt.Errorf("convert binary left: %w", err)
	}

	right, err := c.ConvertExpression(expr.RHS)
	if err != nil {
		return nil, fmt.Errorf("convert binary right: %w", err)
	}
	op := strings.TrimSpace(c.rangeSource(hcl.Range{Filename: expr.SrcRange.Filename, Start: expr.LHS.Range().End, End: expr.RHS.Range().Start}))

	return c.makeExprNode("binary", expr, map[string]interface{}{
		"op":    op,
		"left":  left,
		"right": right,
	}, func() string {
		return sourceFromNode(left) + " " + op + " " + sourceFromNode(right)
	}), nil
}

func (c *converter) convertFunctionCallExpr(expr *hclsyntax.FunctionCallExpr) (jsonObj, error) {
	var args []interface{}
	var argSources []string
	for _, arg := range expr.Args {
		convertedArg, err := c.ConvertExpression(arg)
		if err != nil {
			return nil, fmt.Errorf("convert function arg: %w", err)
		}
		args = append(args, convertedArg)
		argSources = append(argSources, sourceFromNode(convertedArg))
	}
	name := c.rangeSource(expr.NameRange)

	return c.makeExprNode("function", expr, map[string]interface{}{
		"name": name,
		"args": args,
	}, func() string {
		return name + "(" + strings.Join(argSources, ", ") + ")"
	}), nil
}

func (c *converter) convertTemplateExpr(expr *hclsyntax.TemplateExpr) (jsonObj, error) {
	if expr.IsStringLiteral() {
		v, err := expr.Value(nil)
		if err != nil {
			return nil, err
		}
		return c.ConvertExpression(&hclsyntax.LiteralValueExpr{Val: v, SrcRange: expr.SrcRange})
	}

	var parts []interface{}
	for _, part := range expr.Parts {
		s, err := c.ConvertExpression(part)
		if err != nil {
			return nil, err
		}
		parts = append(parts, s)
	}

	return c.makeExprNode("template", expr, map[string]interface{}{
		"parts": parts,
	}, func() string {
		return strings.TrimSpace(c.rangeSource(expr.Range()))
	}), nil
}

func (c *converter) convertTemplateWrapExpr(expr *hclsyntax.TemplateWrapExpr) (jsonObj, error) {
	return c.ConvertExpression(expr.Wrapped)
}

func (c *converter) convertTemplateJoinExpr(expr *hclsyntax.TemplateJoinExpr) (jsonObj, error) {
	convertedExpr, err := c.ConvertExpression(expr.Tuple)
	if err != nil {
		return nil, fmt.Errorf("convert template join tuple: %w", err)
	}

	return c.makeExprNode("template-join", expr, map[string]interface{}{
		"expr": convertedExpr,
	}, func() string {
		return strings.TrimSpace(c.rangeSource(expr.Range()))
	}), nil
}

func (c *converter) convertConditionalExpr(expr *hclsyntax.ConditionalExpr) (jsonObj, error) {
	cond, err := c.ConvertExpression(expr.Condition)
	if err != nil {
		return nil, fmt.Errorf("convert conditional condition: %w", err)
	}

	trueResult, err := c.ConvertExpression(expr.TrueResult)
	if err != nil {
		return nil, fmt.Errorf("convert conditional true result: %w", err)
	}

	falseResult, err := c.ConvertExpression(expr.FalseResult)
	if err != nil {
		return nil, fmt.Errorf("convert conditional false result: %w", err)
	}

	return c.makeExprNode("conditional", expr, map[string]interface{}{
		"condition": cond,
		"ifTrue":    trueResult,
		"ifFalse":   falseResult,
	}, func() string {
		return sourceFromNode(cond) + " ? " + sourceFromNode(trueResult) + " : " + sourceFromNode(falseResult)
	}), nil
}

func (c *converter) convertForExpr(expr *hclsyntax.ForExpr) (jsonObj, error) {
	coll, err := c.ConvertExpression(expr.CollExpr)
	if err != nil {
		return nil, fmt.Errorf("convert for coll: %w", err)
	}

	valExpr, err := c.ConvertExpression(expr.ValExpr)
	if err != nil {
		return nil, fmt.Errorf("convert for valExpr: %w", err)
	}

	fields := map[string]interface{}{
		"valVar":     expr.ValVar,
		"collection": coll,
		"valExpr":    valExpr,
	}

	if len(expr.KeyVar) > 0 {
		fields["keyVar"] = expr.KeyVar
	} else {
		fields["keyVar"] = nil
	}

	if expr.KeyExpr != nil {
		keyExpr, err := c.ConvertExpression(expr.KeyExpr)
		if err != nil {
			return nil, fmt.Errorf("convert for keyExpr: %w", err)
		}
		fields["keyExpr"] = keyExpr
	} else {
		fields["keyExpr"] = nil
	}

	if expr.CondExpr != nil {
		condExpr, err := c.ConvertExpression(expr.CondExpr)
		if err != nil {
			return nil, fmt.Errorf("convert for condExpr: %w", err)
		}
		fields["condExpr"] = condExpr
	} else {
		fields["condExpr"] = nil
	}

	return c.makeExprNode("for", expr, fields, func() string {
		var out strings.Builder
		if expr.KeyExpr == nil {
			out.WriteString("[")
		} else {
			out.WriteString("{")
		}
		out.WriteString("for ")
		if len(expr.KeyVar) > 0 {
			out.WriteString(expr.KeyVar)
			out.WriteString(", ")
		}
		out.WriteString(expr.ValVar)
		out.WriteString(" in ")
		out.WriteString(sourceFromNode(coll))
		out.WriteString(" : ")
		if fields["keyExpr"] != nil {
			out.WriteString(sourceFromNode(fields["keyExpr"].(jsonObj)))
			out.WriteString(" => ")
		}
		out.WriteString(sourceFromNode(valExpr))
		if fields["condExpr"] != nil {
			out.WriteString(" if ")
			out.WriteString(sourceFromNode(fields["condExpr"].(jsonObj)))
		}
		if expr.KeyExpr == nil {
			out.WriteString("]")
		} else {
			out.WriteString("}")
		}
		return out.String()
	}), nil
}

func (c *converter) convertIndexExpr(expr *hclsyntax.IndexExpr) (jsonObj, error) {
	collection, err := c.ConvertExpression(expr.Collection)
	if err != nil {
		return nil, fmt.Errorf("convert index collection: %w", err)
	}

	key, err := c.ConvertExpression(expr.Key)
	if err != nil {
		return nil, fmt.Errorf("convert index key: %w", err)
	}

	return c.makeExprNode("index", expr, map[string]interface{}{
		"collection": collection,
		"key":        key,
	}, func() string {
		return sourceFromNode(collection) + "[" + sourceFromNode(key) + "]"
	}), nil
}

func (c *converter) convertTraversalParts(traversal hcl.Traversal, kind string) ([]interface{}, error) {
	var parts []interface{}
	for _, part := range traversal {
		switch p := part.(type) {
		case hcl.TraverseAttr:
			parts = append(parts, map[string]interface{}{
				"type": "attr",
				"name": p.Name,
			})
		case hcl.TraverseIndex:
			key, err := c.ConvertExpression(&hclsyntax.LiteralValueExpr{Val: p.Key, SrcRange: p.SrcRange})
			if err != nil {
				return nil, fmt.Errorf("convert %s traversal index key: %w", kind, err)
			}
			parts = append(parts, map[string]interface{}{
				"type": "index",
				"key":  key,
			})
		case hcl.TraverseRoot:
			parts = append(parts, map[string]interface{}{
				"type": "root",
				"name": p.Name,
			})
		default:
			return nil, fmt.Errorf("unknown %s traversal part type: %T", kind, p)
		}
	}

	return parts, nil
}

func (c *converter) convertScopeTraversalExpr(expr *hclsyntax.ScopeTraversalExpr) (jsonObj, error) {
	parts, err := c.convertTraversalParts(expr.Traversal, "scope")
	if err != nil {
		return nil, err
	}

	return c.makeExprNode("scope-traversal", expr, map[string]interface{}{
		"parts": parts,
	}, func() string {
		var out strings.Builder
		for i, part := range expr.Traversal {
			out.WriteString(c.traversalPartSource(part, i == 0))
		}
		return out.String()
	}), nil
}

func (c *converter) convertRelativeTraversalExpr(expr *hclsyntax.RelativeTraversalExpr) (jsonObj, error) {
	parts, err := c.convertTraversalParts(expr.Traversal, "relative")
	if err != nil {
		return nil, err
	}
	sourceExpr, err := c.ConvertExpression(expr.Source)
	if err != nil {
		return nil, fmt.Errorf("convert relative traversal source: %w", err)
	}

	return c.makeExprNode("relative-traversal", expr, map[string]interface{}{
		"parts": parts,
	}, func() string {
		var out strings.Builder
		out.WriteString(sourceFromNode(sourceExpr))
		for _, part := range expr.Traversal {
			out.WriteString(c.traversalPartSource(part, false))
		}
		return out.String()
	}), nil
}

func (c *converter) convertAnonSymbolExpr(expr *hclsyntax.AnonSymbolExpr) (jsonObj, error) {
	return c.makeExprNode("anon-symbol", expr, map[string]interface{}{}, func() string {
		return "*"
	}), nil
}

func (c *converter) convertSplatExpr(expr *hclsyntax.SplatExpr) (jsonObj, error) {
	sourceExpr, err := c.ConvertExpression(expr.Source)
	if err != nil {
		return nil, fmt.Errorf("convert splat source: %w", err)
	}

	eachExpr, err := c.ConvertExpression(expr.Each)
	if err != nil {
		return nil, fmt.Errorf("convert splat each: %w", err)
	}

	itemExpr, err := c.ConvertExpression(expr.Item)
	if err != nil {
		return nil, fmt.Errorf("convert splat item: %w", err)
	}

	marker := strings.TrimSpace(c.rangeSource(expr.MarkerRange))

	return c.makeExprNode("splat", expr, map[string]interface{}{
		"source": sourceExpr,
		"each":   eachExpr,
		"item":   itemExpr,
	}, func() string {
		eachSource := sourceFromNode(eachExpr)
		if eachSource == "*" {
			return sourceFromNode(sourceExpr) + marker
		}
		suffix := eachSource
		suffix = strings.TrimPrefix(suffix, "[*]")
		suffix = strings.TrimPrefix(suffix, ".*")
		suffix = strings.TrimPrefix(suffix, "*")
		return sourceFromNode(sourceExpr) + marker + suffix
	}), nil
}

func (c *converter) convertParenthesesExpr(expr *hclsyntax.ParenthesesExpr) (jsonObj, error) {
	inner, err := c.ConvertExpression(expr.Expression)
	if err != nil {
		return nil, fmt.Errorf("convert parentheses expr: %w", err)
	}

	return c.makeExprNode("parentheses", expr, map[string]interface{}{
		"expr": inner,
	}, func() string {
		return "(" + sourceFromNode(inner) + ")"
	}), nil
}

func (c *converter) ConvertExpression(expr hclsyntax.Expression) (jsonObj, error) {
	// assume it is hcl syntax (because, um, it is)
	switch value := expr.(type) {
	// Literals
	case *hclsyntax.LiteralValueExpr:
		return c.convertLiteralValueExpr(value)
	case *hclsyntax.TupleConsExpr:
		return c.convertTupleConsExpr(value)
	case *hclsyntax.ObjectConsExpr:
		return c.convertObjectConsExpr(value)

	// Operators + Functions
	case *hclsyntax.UnaryOpExpr:
		return c.convertUnaryOpExpr(value)
	case *hclsyntax.BinaryOpExpr:
		return c.convertBinaryOpExpr(value)
	case *hclsyntax.FunctionCallExpr:
		return c.convertFunctionCallExpr(value)

	// Templating
	case *hclsyntax.TemplateExpr:
		return c.convertTemplateExpr(value)
	case *hclsyntax.TemplateWrapExpr:
		return c.convertTemplateWrapExpr(value)
	case *hclsyntax.TemplateJoinExpr:
		return c.convertTemplateJoinExpr(value)

	// "Control Flow"
	case *hclsyntax.ConditionalExpr:
		return c.convertConditionalExpr(value)
	case *hclsyntax.ForExpr:
		return c.convertForExpr(value)

	// Traversal
	case *hclsyntax.IndexExpr:
		return c.convertIndexExpr(value)
	case *hclsyntax.ScopeTraversalExpr:
		return c.convertScopeTraversalExpr(value)
	case *hclsyntax.RelativeTraversalExpr:
		return c.convertRelativeTraversalExpr(value)
	case *hclsyntax.AnonSymbolExpr:
		return c.convertAnonSymbolExpr(value)
	case *hclsyntax.SplatExpr:
		return c.convertSplatExpr(value)
	case *hclsyntax.ParenthesesExpr:
		return c.convertParenthesesExpr(value)
	}

	return nil, fmt.Errorf("unsupported expression type: %T", expr)
}
