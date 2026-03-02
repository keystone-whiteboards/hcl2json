package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

type Options struct {
}

// Bytes takes the contents of an HCL file, as bytes, and converts
// them into a JSON representation of the HCL file.
func Bytes(bytes []byte, filename string, options Options) ([]byte, error) {
	file, diags := hclsyntax.ParseConfig(bytes, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse config: %v", diags.Errs())
	}

	hclBytes, err := File(file, options)
	if err != nil {
		return nil, fmt.Errorf("convert to HCL: %w", err)
	}

	return hclBytes, nil
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

	n := makeNode("file", map[string]interface{}{})
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

func makeExprNode(exprType string, expr hclsyntax.Expression, value map[string]interface{}) jsonObj {
	n := make(jsonObj)
	n["$type"] = exprType
	n["$range"] = makeRange(expr.Range())
	for k, v := range value {
		n[k] = v
	}
	return n
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
	return makeExprNode("literal", expr, map[string]interface{}{
		"value": ctyjson.SimpleJSONValue{Value: expr.Val},
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

	return makeExprNode("tuple", expr, map[string]interface{}{
		"items": items,
	}), nil
}

func (c *converter) convertObjectConsExpr(expr *hclsyntax.ObjectConsExpr) (jsonObj, error) {
	var props []jsonObj
	for _, item := range expr.Items {
		keyExpr := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
		wrappedExpr := keyExpr.Wrapped
		if _, isTraversal := wrappedExpr.(*hclsyntax.ScopeTraversalExpr); isTraversal && !keyExpr.ForceNonLiteral {
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
	}

	return makeExprNode("object", expr, map[string]interface{}{
		"props": props,
	}), nil
}

func (c *converter) convertUnaryOpExpr(expr *hclsyntax.UnaryOpExpr) (jsonObj, error) {
	arg, err := c.ConvertExpression(expr.Val)
	if err != nil {
		return nil, fmt.Errorf("convert unary arg: %w", err)
	}

	return makeExprNode("unary", expr, map[string]interface{}{
		"op":  c.rangeSource(expr.SymbolRange),
		"arg": arg,
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

	return makeExprNode("binary", expr, map[string]interface{}{
		"op":    strings.TrimSpace(c.rangeSource(hcl.Range{Filename: expr.SrcRange.Filename, Start: expr.LHS.Range().End, End: expr.RHS.Range().Start})),
		"left":  left,
		"right": right,
	}), nil
}

func (c *converter) convertFunctionCallExpr(expr *hclsyntax.FunctionCallExpr) (jsonObj, error) {
	var args []interface{}
	for _, arg := range expr.Args {
		convertedArg, err := c.ConvertExpression(arg)
		if err != nil {
			return nil, fmt.Errorf("convert function arg: %w", err)
		}
		args = append(args, convertedArg)
	}

	return makeExprNode("function", expr, map[string]interface{}{
		"name": c.rangeSource(expr.NameRange),
		"args": args,
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

	return makeExprNode("template", expr, map[string]interface{}{
		"parts": parts,
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

	return makeExprNode("template-join", expr, map[string]interface{}{
		"expr": convertedExpr,
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

	return makeExprNode("conditional", expr, map[string]interface{}{
		"condition": cond,
		"ifTrue":    trueResult,
		"ifFalse":   falseResult,
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

	return makeExprNode("for", expr, fields), nil
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

	return makeExprNode("index", expr, map[string]interface{}{
		"collection": collection,
		"key":        key,
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

	return makeExprNode("scope-traversal", expr, map[string]interface{}{
		"parts": parts,
	}), nil
}

func (c *converter) convertRelativeTraversalExpr(expr *hclsyntax.RelativeTraversalExpr) (jsonObj, error) {
	parts, err := c.convertTraversalParts(expr.Traversal, "relative")
	if err != nil {
		return nil, err
	}

	return makeExprNode("relative-traversal", expr, map[string]interface{}{
		"parts": parts,
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
	}

	return nil, fmt.Errorf("unsupported expression type: %T", expr)
}
