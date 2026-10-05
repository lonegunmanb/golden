package golden

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
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
	source := []byte("marked_decode \"test\" \"example\" {\n  http {\n" + attributes + "\n  }\n}")
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
		wantError  bool
	}{
		{"unmarked pointer", "bearer_token = var.token", cty.StringVal(credential), false},
		{"marked pointer", "bearer_token = var.token", cty.StringVal(credential).Mark("sensitive"), true},
		{"marked string", "credential = var.token", cty.StringVal(credential).Mark("sensitive"), true},
		{"unmarked null pointer", "bearer_token = var.token", cty.NullVal(cty.String), false},
		{"marked null pointer", "bearer_token = var.token", cty.NullVal(cty.String).Mark("sensitive"), false},
		{"marked cty value", "raw = var.token", cty.StringVal(credential).Mark("sensitive"), false},
		{"marked null cty value", "raw = var.token", cty.NullVal(cty.String).Mark("sensitive"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := newMarkedDecodeBlock(t, tc.attributes, tc.token)
			var err error
			assert.NotPanics(t, func() { err = Decode(block) })
			if tc.wantError {
				require.Error(t, err)
				assert.ErrorContains(t, err, "CustomDecode")
				assert.NotContains(t, err.Error(), credential)
				return
			}
			require.NoError(t, err)
			require.Len(t, block.HTTP, 1)
			switch tc.name {
			case "unmarked pointer":
				require.NotNil(t, block.HTTP[0].BearerToken)
				assert.Equal(t, credential, *block.HTTP[0].BearerToken)
			case "unmarked null pointer", "marked null pointer":
				assert.Nil(t, block.HTTP[0].BearerToken)
			case "marked cty value", "marked null cty value":
				assert.True(t, block.HTTP[0].Raw.IsMarked())
				assert.Equal(t, tc.token, block.HTTP[0].Raw)
			}
		})
	}
}
