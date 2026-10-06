package golden

import (
	"reflect"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

type markedDecodeHTTP struct {
	Credential  string    `hcl:"credential,optional"`
	BearerToken *string   `hcl:"bearer_token,optional"`
	Raw         cty.Value `hcl:"raw,optional"`
}

type markedDecodeBlock struct {
	*BaseBlock
	HTTP  []markedDecodeHTTP `hcl:"http,block"`
	token cty.Value
}

func (*markedDecodeBlock) Type() string            { return "test" }
func (*markedDecodeBlock) BlockType() string       { return "marked_decode" }
func (*markedDecodeBlock) AddressLength() int      { return 3 }
func (*markedDecodeBlock) CanExecutePrePlan() bool { return false }

func (b *markedDecodeBlock) EvalContext() *hcl.EvalContext {
	return &hcl.EvalContext{Variables: map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{"token": b.token}),
	}}
}

func newMarkedDecodeBlock(t *testing.T, attributes string, token cty.Value) *markedDecodeBlock {
	t.Helper()
	return newMarkedDecodeBlockBody(t, "  http {\n"+attributes+"\n  }", token)
}

func newMarkedDecodeBlockBody(t *testing.T, body string, token cty.Value) *markedDecodeBlock {
	t.Helper()
	source := []byte("marked_decode \"test\" \"example\" {\n" + body + "\n}")
	parsed, diags := hclsyntax.ParseConfig(source, "marked_decode.hcl", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "%s", diags)
	written, diags := hclwrite.ParseConfig(source, "marked_decode.hcl", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "%s", diags)
	hb := NewHclBlock(parsed.Body.(*hclsyntax.Body).Blocks[0], written.Body().Blocks()[0], nil)
	return &markedDecodeBlock{BaseBlock: NewBaseBlock(nil, hb), token: token}
}

func TestDecodeMarkedNestedCredentials(t *testing.T) {
	RegisterBlock(&markedDecodeBlock{})
	const credential = "fake-credential"
	for _, tc := range []struct {
		name       string
		attributes string
		token      cty.Value
	}{
		{"unmarked pointer", "bearer_token = var.token", cty.StringVal(credential)},
		{"marked pointer", "bearer_token = var.token", cty.StringVal(credential).Mark("sensitive")},
		{"marked string", "credential = var.token", cty.StringVal(credential).Mark("sensitive")},
		{"marked number converted to string", "bearer_token = var.token", cty.NumberIntVal(42).Mark("sensitive")},
		{"unmarked null pointer", "bearer_token = var.token", cty.NullVal(cty.String)},
		{"marked null pointer", "bearer_token = var.token", cty.NullVal(cty.String).Mark("sensitive")},
		{"marked cty value", "raw = var.token", cty.StringVal(credential).Mark("sensitive")},
		{"marked null cty value", "raw = var.token", cty.NullVal(cty.String).Mark("sensitive")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := newMarkedDecodeBlock(t, tc.attributes, tc.token)
			var err error
			require.NotPanics(t, func() { err = Decode(block) })
			require.NoError(t, err)
			require.Len(t, block.HTTP, 1)
			reflected := Value(block)["http"].Index(cty.NumberIntVal(0))
			switch tc.name {
			case "unmarked pointer":
				require.NotNil(t, block.HTTP[0].BearerToken)
				assert.Equal(t, credential, *block.HTTP[0].BearerToken)
				assert.False(t, reflected.GetAttr("bearer_token").IsMarked())
			case "marked pointer":
				require.NotNil(t, block.HTTP[0].BearerToken)
				assert.Equal(t, credential, *block.HTTP[0].BearerToken)
				assert.True(t, reflected.GetAttr("bearer_token").IsMarked())
				assert.NotContains(t, BlockToString(block), credential)
				assert.True(t, blockToCtyValue(block).GetAttr("http").Index(cty.NumberIntVal(0)).GetAttr("bearer_token").IsMarked())
			case "marked string":
				assert.Equal(t, credential, block.HTTP[0].Credential)
				assert.True(t, reflected.GetAttr("credential").IsMarked())
				assert.NotContains(t, BlockToString(block), credential)
			case "marked number converted to string":
				require.NotNil(t, block.HTTP[0].BearerToken)
				assert.Equal(t, "42", *block.HTTP[0].BearerToken)
				assert.True(t, reflected.GetAttr("bearer_token").IsMarked())
			case "unmarked null pointer", "marked null pointer":
				assert.Nil(t, block.HTTP[0].BearerToken)
			case "marked cty value", "marked null cty value":
				assert.True(t, block.HTTP[0].Raw.IsMarked())
				assert.Equal(t, tc.token, block.HTTP[0].Raw)
			}
		})
	}
}

func TestDecodeClearsStaleStringMarks(t *testing.T) {
	RegisterBlock(&markedDecodeBlock{})
	block := newMarkedDecodeBlock(t, "bearer_token = var.token", cty.StringVal("fake-credential").Mark("sensitive"))
	require.NoError(t, Decode(block))
	block.token = cty.StringVal("new-value")
	require.NoError(t, Decode(block))
	require.NotNil(t, block.HTTP[0].BearerToken)
	assert.Equal(t, "new-value", *block.HTTP[0].BearerToken)
	assert.False(t, Value(block)["http"].Index(cty.NumberIntVal(0)).GetAttr("bearer_token").IsMarked())
}

func TestDecodeMarkedStringMultipleBlocks(t *testing.T) {
	RegisterBlock(&markedDecodeBlock{})
	block := newMarkedDecodeBlockBody(t, `  http {
    credential = "public"
    bearer_token = var.token
	  raw = var.token
	}
	http {
	  credential = var.token
	  bearer_token = var.token
	  raw = var.token
  }`, cty.StringVal("fake-credential").Mark("sensitive"))
	require.NoError(t, Decode(block))
	require.Len(t, block.HTTP, 2)
	require.NotNil(t, block.HTTP[0].BearerToken)
	assert.Equal(t, "fake-credential", *block.HTTP[0].BearerToken)
	assert.Equal(t, "fake-credential", block.HTTP[1].Credential)
	http := Value(block)["http"]
	assert.False(t, http.Index(cty.NumberIntVal(0)).GetAttr("credential").IsMarked())
	assert.True(t, http.Index(cty.NumberIntVal(0)).GetAttr("bearer_token").IsMarked())
	assert.True(t, http.Index(cty.NumberIntVal(1)).GetAttr("credential").IsMarked())
	assert.True(t, http.Index(cty.NumberIntVal(1)).GetAttr("raw").IsMarked())
	assert.NotContains(t, BlockToString(block), "fake-credential")
}

func TestDecodeMarkedStringWithOtherDiagnosticDoesNotLeak(t *testing.T) {
	RegisterBlock(&markedDecodeBlock{})
	block := newMarkedDecodeBlock(t, "bearer_token = var.token\n    unexpected = true", cty.StringVal("fake-credential").Mark("sensitive"))
	err := Decode(block)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "fake-credential")
	assert.NotContains(t, BlockToString(block), "fake-credential")
}

func TestBlockToStringBeforeDecode(t *testing.T) {
	assert.NotPanics(t, func() { BlockToString(&markedDecodeBlock{}) })
	assert.NotPanics(t, func() { Value(&markedDecodeBlock{}) })
}

func TestMarkedStringBodyExplicitAttrTag(t *testing.T) {
	type target struct {
		Credential string `hcl:"credential,attr"`
	}
	parsed, diags := hclsyntax.ParseConfig([]byte("credential = var.token"), "marked.hcl", hcl.InitialPos)
	require.False(t, diags.HasErrors())
	var marks []cty.PathValueMarks
	body := markedStringBody{Body: parsed.Body, target: reflect.TypeOf(target{}), marks: &marks}
	var decoded target
	diags = gohcl.DecodeBody(body, &hcl.EvalContext{Variables: map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{"token": cty.StringVal("fake-credential").Mark("sensitive")}),
	}}, &decoded)
	require.False(t, diags.HasErrors(), "%s", diags)
	assert.Equal(t, "fake-credential", decoded.Credential)
	require.Len(t, marks, 1)
	assert.True(t, marks[0].Path.Equals(cty.Path{}.GetAttr("credential")))
}

func TestMarkedStringBodyRemainField(t *testing.T) {
	type remainder struct {
		Credential string `hcl:"credential,attr"`
	}
	type target struct {
		Known string    `hcl:"known,attr"`
		Extra remainder `hcl:",remain"`
	}
	parsed, diags := hclsyntax.ParseConfig([]byte("known = \"public\"\ncredential = var.token"), "marked_remain.hcl", hcl.InitialPos)
	require.False(t, diags.HasErrors())
	var marks []cty.PathValueMarks
	body := markedStringBody{Body: parsed.Body, target: reflect.TypeOf(target{}), marks: &marks}
	var decoded target
	diags = gohcl.DecodeBody(body, &hcl.EvalContext{Variables: map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{"token": cty.StringVal("fake-credential").Mark("sensitive")}),
	}}, &decoded)
	require.False(t, diags.HasErrors(), "%s", diags)
	assert.Equal(t, "fake-credential", decoded.Extra.Credential)
	require.Len(t, marks, 1)
	assert.True(t, marks[0].Path.Equals(cty.Path{}.GetAttr("").GetAttr("credential")))
}
