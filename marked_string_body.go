package golden

import (
	"reflect"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// markedStringBody unmarks only string expressions destined for Go fields.
// The original evaluation context and syntax tree remain marked.
type markedStringBody struct {
	hcl.Body
	target reflect.Type
	path   cty.Path
	marks  *[]cty.PathValueMarks
}

func (body markedStringBody) Content(schema *hcl.BodySchema) (*hcl.BodyContent, hcl.Diagnostics) {
	content, diags := body.Body.Content(schema)
	return body.wrapContent(content), diags
}

func (body markedStringBody) PartialContent(schema *hcl.BodySchema) (*hcl.BodyContent, hcl.Body, hcl.Diagnostics) {
	content, remaining, diags := body.Body.PartialContent(schema)
	if remaining != nil {
		remaining = markedStringBody{Body: remaining, marks: body.marks}
	}
	return body.wrapContent(content), remaining, diags
}

func (body markedStringBody) wrapContent(content *hcl.BodyContent) *hcl.BodyContent {
	if content == nil || body.target == nil || body.target.Kind() != reflect.Struct {
		return content
	}
	wrapped := *content
	wrapped.Attributes = make(hcl.Attributes, len(content.Attributes))
	for name, attr := range content.Attributes {
		wrapped.Attributes[name] = attr
		field, ok := markedDecodeField(body.target, name, "")
		if !ok || !isGoString(field.Type) {
			continue
		}
		copyAttr := *attr
		copyAttr.Expr = markedStringExpr{Expression: attr.Expr, path: body.path.GetAttr(outputFieldName(field)), marks: body.marks}
		wrapped.Attributes[name] = &copyAttr
	}

	wrapped.Blocks = make(hcl.Blocks, len(content.Blocks))
	indices := make(map[string]int)
	for i, block := range content.Blocks {
		wrapped.Blocks[i] = block
		field, ok := markedDecodeField(body.target, block.Type, "block")
		if !ok {
			continue
		}
		ty := field.Type
		nextPath := body.path
		if ty.Kind() == reflect.Slice {
			nextPath = nextPath.GetAttr(outputFieldName(field)).IndexInt(indices[block.Type])
			ty = ty.Elem()
		} else {
			nextPath = nextPath.GetAttr(outputFieldName(field))
		}
		indices[block.Type]++
		if ty.Kind() == reflect.Ptr {
			ty = ty.Elem()
		}
		copyBlock := *block
		copyBlock.Body = markedStringBody{Body: block.Body, target: ty, path: nextPath, marks: body.marks}
		wrapped.Blocks[i] = &copyBlock
	}
	return &wrapped
}

func outputFieldName(field reflect.StructField) string {
	name, _ := fieldName(field)
	return name
}

func markedDecodeField(ty reflect.Type, name, kind string) (reflect.StructField, bool) {
	for i := 0; i < ty.NumField(); i++ {
		field := ty.Field(i)
		tag := strings.Split(field.Tag.Get("hcl"), ",")
		if tag[0] != name {
			continue
		}
		if kind == "" && (len(tag) == 1 || len(tag) == 2 && tag[1] == "optional") {
			return field, true
		}
		for _, part := range tag[1:] {
			if part == kind {
				return field, true
			}
		}
	}
	return reflect.StructField{}, false
}

func isGoString(ty reflect.Type) bool {
	if ty.Kind() == reflect.Ptr {
		ty = ty.Elem()
	}
	return ty.Kind() == reflect.String
}

type markedStringExpr struct {
	hcl.Expression
	path  cty.Path
	marks *[]cty.PathValueMarks
}

func (expr markedStringExpr) Value(ctx *hcl.EvalContext) (cty.Value, hcl.Diagnostics) {
	value, diags := expr.Expression.Value(ctx)
	if value.Type() != cty.String || !value.IsMarked() || value.IsNull() {
		return value, diags
	}
	unmarked, marks := value.Unmark()
	*expr.marks = append(*expr.marks, cty.PathValueMarks{Path: expr.path, Marks: marks})
	return unmarked, diags
}
