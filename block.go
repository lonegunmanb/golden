package golden

import (
	"encoding/json"
	"fmt"
	"github.com/emirpasic/gods/sets/hashset"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/lonegunmanb/go-defaults"
	"github.com/zclconf/go-cty/cty"
	"strings"
)

type BlockType interface {
	BlockType() string
}

type Block interface {
	Id() string
	Name() string
	Type() string
	BlockType() string
	Address() string
	HclBlock() *HclBlock
	EvalContext() *hcl.EvalContext
	BaseValues() map[string]cty.Value
	PreConditionCheck(*hcl.EvalContext) ([]PreCondition, error)
	AddressLength() int
	CanExecutePrePlan() bool
	getDownstreams() []Block
	getForEach() *ForEach
	markExpanded()
	isReadyForRead() bool
	markReady()
	expandable() bool
}

func BlockToString(f Block) string {
	marshal, _ := json.Marshal(f)
	return string(marshal)
}

var metaAttributeNames = hashset.New("for_each", "rule_ids")
var metaNestedBlockNames = hashset.New("precondition")

func Decode(b Block) error {
	hb := b.HclBlock()
	evalContext := b.EvalContext()
	if decodeBase, ok := b.(CustomDecodeBase); ok {
		err := decodeBase.Decode(hb, evalContext)
		if err != nil {
			return err
		}
	}
	diag := gohcl.DecodeBody(cleanBodyForDecode(hb), evalContext, b)
	if diag.HasErrors() {
		return diag
	}
	// we need set defaults again, since gohcl.DecodeBody might erase default value set on those attribute has null values.
	defaults.SetDefaults(b)
	return nil
}

func cleanBodyForDecode(hb *HclBlock) *hclsyntax.Body {
	// Create a new hclsyntax.Body
	newBody := &hclsyntax.Body{
		Attributes: make(hclsyntax.Attributes),
	}

	// Iterate over the attributes of the original body
	for attrName, attr := range hb.Body.Attributes {
		if metaAttributeNames.Contains(attrName) {
			continue
		}
		newBody.Attributes[attrName] = attr
	}

	for _, nb := range hb.Body.Blocks {
		if metaNestedBlockNames.Contains(nb.Type) {
			continue
		}
		newBody.Blocks = append(newBody.Blocks, nb)
	}

	return newBody
}

func SingleValues(blocks []SingleValueBlock) cty.Value {
	if len(blocks) == 0 {
		return cty.EmptyObjectVal
	}
	res := map[string]cty.Value{}
	for _, b := range blocks {
		res[b.Name()] = b.Value()
	}
	return cty.ObjectVal(res)
}

func Values[T Block](blocks []T) cty.Value {
	if len(blocks) == 0 {
		return cty.EmptyObjectVal
	}
	res := map[string]cty.Value{}
	valuesMap := map[string]map[string]cty.Value{}

	for _, b := range blocks {
		values, exists := valuesMap[b.Type()]
		if !exists {
			values = map[string]cty.Value{}
			valuesMap[b.Type()] = values
		}
		blockVal := blockToCtyValue(b)
		forEach := b.getForEach()
		if forEach == nil {
			values[b.Name()] = blockVal
		} else {
			m, ok := values[b.Name()]
			if !ok {
				m = cty.MapValEmpty(cty.EmptyObject)
			}
			nm := m.AsValueMap()
			if nm == nil {
				nm = make(map[string]cty.Value)
			}
			nm[CtyValueToString(forEach.key)] = blockVal
			values[b.Name()] = cty.MapVal(nm)
		}
		valuesMap[b.Type()] = values
	}
	for t, m := range valuesMap {
		res[t] = cty.ObjectVal(m)
	}
	return cty.ObjectVal(res)
}

func blockToCtyValue(b Block) cty.Value {
	blockValues := map[string]cty.Value{}
	baseCtyValues := b.BaseValues()
	ctyValues := Value(b) //.Values()
	for k, v := range ctyValues {
		blockValues[k] = v
	}
	for k, v := range baseCtyValues {
		blockValues[k] = v
	}
	blockVal := cty.ObjectVal(blockValues)
	return blockVal
}

func concatLabels(labels []string) string {
	sb := strings.Builder{}
	for i, l := range labels {
		if l == "" {
			continue
		}
		sb.WriteString(l)
		if i != len(labels)-1 {
			sb.WriteString(".")
		}
	}
	return sb.String()
}

func blockAddress(b *HclBlock) string {
	sb := strings.Builder{}
	sb.WriteString(b.Block.Type)
	sb.WriteString(".")
	sb.WriteString(concatLabels(b.Block.Labels))
	if b.ForEach != nil {
		sb.WriteString(fmt.Sprintf("[%s]", CtyValueToString(b.ForEach.key)))
	}
	return sb.String()
}

// Not all `local` expression could be evaluated before for_each expansion, so we need to try to evaluate them.
func prePlan(b Block) error {
	l, ok := b.(PrePlanBlock)
	if !ok {
		return nil
	}
	err := l.ExecuteBeforePlan()
	if err != nil {
		return err
	}
	b.markReady()
	return nil
}
