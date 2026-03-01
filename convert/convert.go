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

	out, err := c.ConvertBody(body)
	if err != nil {
		return nil, fmt.Errorf("convert body: %w", err)
	}

	return out, nil
}

func (c *converter) ConvertBody(body *hclsyntax.Body) (jsonObj, error) {
	blocks := make(jsonObj)
	for _, block := range body.Blocks {
		if err := c.convertBlock(block, blocks); err != nil {
			return nil, fmt.Errorf("convert block: %w", err)
		}
	}

	attrs := make(jsonObj)
	var err error
	for key, value := range body.Attributes {
		attrs[key], err = c.ConvertExpression(value.Expr)
		if err != nil {
			return nil, fmt.Errorf("convert expression: %w", err)
		}
	}

	out := make(jsonObj)
	out["blocks"] = blocks
	out["attributes"] = attrs
	return out, nil
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

func (c *converter) convertBlock(block *hclsyntax.Block, out jsonObj) error {
	key := block.Type

	value, err := c.ConvertBody(block.Body)
	if err != nil {
		return fmt.Errorf("convert body: %w", err)
	}
	value["labels"] = block.Labels

	// Multiple blocks can exist with the same name, at the same
	// level in the JSON document (e.g. locals).
	//
	// For consistency, always wrap the value in a collection.
	// When multiple values are at the same key
	if current, exists := out[key]; exists {
		switch currentTyped := current.(type) {
		case []interface{}:
			currentTyped = append(currentTyped, value)
			out[key] = currentTyped
		default:
			return fmt.Errorf("invalid HCL detected for %q block, cannot have blocks with and without labels", key)
		}
	} else {
		out[key] = []interface{}{value}
	}

	return nil
}

func (c *converter) ConvertExpression(expr hclsyntax.Expression) (interface{}, error) {
	// assume it is hcl syntax (because, um, it is)
	switch value := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		n := make(jsonObj)
		n["$type"] = "literal"
		n["value"] = ctyjson.SimpleJSONValue{Value: value.Val}
		return n, nil
	case *hclsyntax.UnaryOpExpr:
		n := make(jsonObj)
		n["$type"] = "unary"
		n["op"] = c.rangeSource(value.SymbolRange)
		arg, err := c.ConvertExpression(value.Val)
		if err != nil {
			return nil, fmt.Errorf("convert unary arg: %w", err)
		}
		n["arg"] = arg
		return n, nil
	case *hclsyntax.TemplateExpr:
		if value.IsStringLiteral() {
			// safe because the value is just the string
			v, err := value.Value(nil)
			if err != nil {
				return "", err
			}
			return c.ConvertExpression(&hclsyntax.LiteralValueExpr{Val: v, SrcRange: value.SrcRange})
		}
		n := make(jsonObj)
		n["$type"] = "template"
		var parts []interface{}
		for _, part := range value.Parts {
			s, err := c.ConvertExpression(part)
			if err != nil {
				return nil, err
			}
			parts = append(parts, s)
		}
		n["parts"] = parts
		return n, nil
	case *hclsyntax.TemplateWrapExpr:
		return c.ConvertExpression(value.Wrapped)
	case *hclsyntax.TemplateJoinExpr:
		n := make(jsonObj)
		n["$type"] = "template-join"
		expr, err := c.ConvertExpression(value.Tuple)
		if err != nil {
			return nil, fmt.Errorf("convert template join tuple: %w", err)
		}
		n["expr"] = expr
		return n, nil
	case *hclsyntax.TupleConsExpr:
		list := make([]interface{}, 0)
		for _, ex := range value.Exprs {
			elem, err := c.ConvertExpression(ex)
			if err != nil {
				return nil, err
			}
			list = append(list, elem)
		}
		return list, nil
	case *hclsyntax.ObjectConsExpr:
		n := make(jsonObj)
		n["$type"] = "object"
		var props []jsonObj
		for _, item := range value.Items {
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

			m := make(jsonObj)
			m["key"] = key
			m["value"] = value
			props = append(props, m)
		}
		n["props"] = props
		return n, nil
	case *hclsyntax.ConditionalExpr:
		n := make(jsonObj)
		n["$type"] = "conditional"
		cond, err := c.ConvertExpression(value.Condition)
		if err != nil {
			return nil, fmt.Errorf("convert conditional condition: %w", err)
		}
		n["condition"] = cond
		trueResult, err := c.ConvertExpression(value.TrueResult)
		if err != nil {
			return nil, fmt.Errorf("convert conditional true result: %w", err)
		}
		n["ifTrue"] = trueResult
		falseResult, err := c.ConvertExpression(value.FalseResult)
		if err != nil {
			return nil, fmt.Errorf("convert conditional false result: %w", err)
		}
		n["ifFalse"] = falseResult
		return n, nil
	case *hclsyntax.BinaryOpExpr:
		n := make(jsonObj)
		n["$type"] = "binary"
		n["op"] = strings.TrimSpace(c.rangeSource(hcl.Range{Filename: value.SrcRange.Filename, Start: value.LHS.Range().End, End: value.RHS.Range().Start}))
		left, err := c.ConvertExpression(value.LHS)
		if err != nil {
			return nil, fmt.Errorf("convert binary left: %w", err)
		}
		n["left"] = left
		right, err := c.ConvertExpression(value.RHS)
		if err != nil {
			return nil, fmt.Errorf("convert binary right: %w", err)
		}
		n["right"] = right
		return n, nil
	case *hclsyntax.FunctionCallExpr:
		n := make(jsonObj)
		n["$type"] = "function"
		n["name"] = c.rangeSource(value.NameRange)
		var args []interface{}
		for _, arg := range value.Args {
			convertedArg, err := c.ConvertExpression(arg)
			if err != nil {
				return nil, fmt.Errorf("convert function arg: %w", err)
			}
			args = append(args, convertedArg)
		}
		n["args"] = args
		return n, nil
	case *hclsyntax.ForExpr:
		n := make(jsonObj)
		n["$type"] = "for"
		if len(value.KeyVar) > 0 {
			n["keyVar"] = value.KeyVar
		} else {
			n["keyVar"] = nil
		}
		n["valVar"] = value.ValVar

		coll, err := c.ConvertExpression(value.CollExpr)
		if err != nil {
			return nil, fmt.Errorf("convert for coll: %w", err)
		}
		n["collection"] = coll

		keyExpr, err := c.ConvertExpression(value.KeyExpr)
		if err != nil {
			return nil, fmt.Errorf("convert for keyExpr: %w", err)
		}
		n["keyExpr"] = keyExpr

		valExpr, err := c.ConvertExpression(value.ValExpr)
		if err != nil {
			return nil, fmt.Errorf("convert for valExpr: %w", err)
		}
		n["valExpr"] = valExpr

		if value.CondExpr != nil {
			condExpr, err := c.ConvertExpression(value.CondExpr)
			if err != nil {
				return nil, fmt.Errorf("convert for condExpr: %w", err)
			}
			n["condExpr"] = condExpr
		} else {
			n["condExpr"] = nil
		}

		return n, nil
	case *hclsyntax.IndexExpr:
		n := make(jsonObj)
		n["$type"] = "index"
		collection, err := c.ConvertExpression(value.Collection)
		if err != nil {
			return nil, fmt.Errorf("convert index collection: %w", err)
		}
		n["collection"] = collection
		key, err := c.ConvertExpression(value.Key)
		if err != nil {
			return nil, fmt.Errorf("convert index key: %w", err)
		}
		n["key"] = key
		return n, nil
	case *hclsyntax.ScopeTraversalExpr:
		n := make(jsonObj)
		n["$type"] = "scope_traversal"
		var parts []interface{}
		for _, part := range value.Traversal {
			switch p := part.(type) {
			case hcl.TraverseAttr:
				parts = append(parts, map[string]interface{}{
					"type": "attr",
					"name": p.Name,
				})
			case hcl.TraverseIndex:
				key, err := c.ConvertExpression(&hclsyntax.LiteralValueExpr{Val: p.Key, SrcRange: p.SrcRange})
				if err != nil {
					return nil, fmt.Errorf("convert scope traversal index key: %w", err)
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
				return nil, fmt.Errorf("unknown scope traversal part type: %T", p)
			}
		}
		n["parts"] = parts
		return n, nil
	case *hclsyntax.RelativeTraversalExpr:
		n := make(jsonObj)
		n["$type"] = "relative_traversal"
		var parts []interface{}
		for _, part := range value.Traversal {
			switch p := part.(type) {
			case hcl.TraverseAttr:
				parts = append(parts, map[string]interface{}{
					"type": "attr",
					"name": p.Name,
				})
			case hcl.TraverseIndex:
				key, err := c.ConvertExpression(&hclsyntax.LiteralValueExpr{Val: p.Key, SrcRange: p.SrcRange})
				if err != nil {
					return nil, fmt.Errorf("convert relative traversal index key: %w", err)
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
				return nil, fmt.Errorf("unknown relative traversal part type: %T", p)
			}
		}
		n["parts"] = parts
		return n, nil
	}

	return nil, fmt.Errorf("unsupported expression type: %T", expr)
}
