package golden

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/zclconf/go-cty/cty"
)

type outputSuite struct {
	suite.Suite
	*testBase
}

func TestOutputSuite(t *testing.T) {
	suite.Run(t, new(outputSuite))
}

func (s *outputSuite) SetupTest() {
	s.testBase = newTestBase()
}

func (s *outputSuite) TearDownTest() {
	s.teardown()
}

func (s *outputSuite) buildConfig(code string) Config {
	s.dummyFsWithFiles(map[string]string{
		"test.hcl": code,
	})
	c, err := BuildDummyConfig("/", "", nil, nil)
	require.NoError(s.T(), err)
	return c
}

func (s *outputSuite) onlyOutput(c Config) *OutputBlock {
	outputs := Blocks[*OutputBlock](c)
	require.Len(s.T(), outputs, 1)
	return outputs[0]
}

func (s *outputSuite) TestOutputBlockContract() {
	c := s.buildConfig(`
output "result" {
  value = "hello"
}
`)
	_, err := RunDummyPlan(c)
	require.NoError(s.T(), err)

	output := s.onlyOutput(c)
	assert.Equal(s.T(), "output", output.BlockType())
	assert.Equal(s.T(), "", output.Type())
	assert.Equal(s.T(), 2, output.AddressLength())
	assert.Equal(s.T(), "result", output.Name())
	assert.Equal(s.T(), "output.result", output.Address())
	assert.False(s.T(), output.CanExecutePrePlan())
	assert.Equal(s.T(), "output", output.BlockType())
}

func (s *outputSuite) TestOutputBlockEvaluatesValue() {
	c := s.buildConfig(`
variable "name" {
  default = "world"
}

locals {
  greeting = "hello ${var.name}"
}

output "primitive" {
  value = local.greeting
}

output "null" {
  value = null
}

output "collection" {
  value = [1, 2]
}

output "object" {
  value = { key = "value" }
}
`)
	_, err := RunDummyPlan(c)
	require.NoError(s.T(), err)

	outputs := Blocks[*OutputBlock](c)
	require.Len(s.T(), outputs, 4)
	values := make(map[string]cty.Value)
	for _, output := range outputs {
		values[output.Name()] = output.Value()
	}
	assert.True(s.T(), values["primitive"].RawEquals(cty.StringVal("hello world")))
	assert.True(s.T(), values["null"].IsNull() && values["null"].Type() == cty.DynamicPseudoType)
	assert.True(s.T(), values["collection"].RawEquals(cty.TupleVal([]cty.Value{cty.NumberIntVal(1), cty.NumberIntVal(2)})))
	assert.True(s.T(), values["object"].RawEquals(cty.ObjectVal(map[string]cty.Value{"key": cty.StringVal("value")})))
}

func (s *outputSuite) TestOutputBlockValueIsRequired() {
	s.dummyFsWithFiles(map[string]string{
		"test.hcl": `
output "result" {
}
`,
	})
	c, err := BuildDummyConfig("/", "", nil, nil)
	require.NoError(s.T(), err)
	_, err = RunDummyPlan(c)
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "`value` is required")
}

func (s *outputSuite) TestOutputBlockDescription() {
	c := s.buildConfig(`
output "described" {
  value       = "hello"
  description = "Human-readable description"
}

output "plain" {
  value = "world"
}
`)
	_, err := RunDummyPlan(c)
	require.NoError(s.T(), err)

	outputs := Blocks[*OutputBlock](c)
	require.Len(s.T(), outputs, 2)
	byName := make(map[string]*OutputBlock)
	for _, output := range outputs {
		byName[output.Name()] = output
	}
	require.NotNil(s.T(), byName["described"].Description())
	assert.Equal(s.T(), "Human-readable description", *byName["described"].Description())
	assert.Nil(s.T(), byName["plain"].Description())
}

func (s *outputSuite) TestMarkedDescriptionIsRejected() {
	s.dummyFsWithFiles(map[string]string{
		"test.hcl": `
marked_data "marked" source {
}

output "described" {
  value       = "hello"
  description = marked_data.marked.source.value
}
`,
	})
	RegisterBlock(new(SensitiveMarkedDataBlock))
	c, err := BuildDummyConfig("/", "", nil, nil)
	require.NoError(s.T(), err)
	_, err = RunDummyPlan(c)
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "`description` must not be a marked value")
}

func (s *outputSuite) TestMarkedSensitiveFlagIsUnmarkedBeforeConversion() {
	s.dummyFsWithFiles(map[string]string{
		"test.hcl": `
marked_bool "flag" source {
}

output "guarded" {
  value     = "hello"
  sensitive = marked_bool.flag.source.value
}
`,
	})
	RegisterBlock(new(MarkedBoolDataBlock))
	c, err := BuildDummyConfig("/", "", nil, nil)
	require.NoError(s.T(), err)
	_, err = RunDummyPlan(c)
	require.NoError(s.T(), err)

	output := s.onlyOutput(c)
	assert.True(s.T(), output.Sensitive())
	assert.True(s.T(), output.Value().HasMark(SensitiveMark))
	assert.Equal(s.T(), "<sensitive>", CtyValueToString(output.Value()))
}

func (s *outputSuite) TestDynamicPreconditionIsExpandedAndChecked() {
	c := s.buildConfig(`
locals {
  checks = [{ pass = true }, { pass = false }]
}

output "result" {
  value = "hello"
  dynamic "precondition" {
    for_each = local.checks
    content {
      condition     = precondition.value.pass
      error_message = "generated precondition must pass"
    }
  }
}
`)
	_, err := RunDummyPlan(c)
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "generated precondition must pass")
}

func (s *outputSuite) TestOutputBlockRejectsInvalidDescriptionAndSensitive() {
	cases := []struct {
		desc      string
		attribute string
		wantErr   string
	}{
		{desc: "description number", attribute: `description = 1`, wantErr: "incorrect type for `description`"},
		{desc: "description null", attribute: `description = null`, wantErr: "incorrect type for `description`"},
		{desc: "sensitive string", attribute: `sensitive = "true"`, wantErr: "incorrect type for `sensitive`"},
		{desc: "sensitive null", attribute: `sensitive = null`, wantErr: "incorrect type for `sensitive`"},
		{desc: "sensitive number", attribute: `sensitive = 1`, wantErr: "incorrect type for `sensitive`"},
	}
	for _, c := range cases {
		s.Run(c.desc, func() {
			code := fmt.Sprintf(`
output "result" {
  value = "hello"
  %s
}
`, c.attribute)
			s.dummyFsWithFiles(map[string]string{
				"test.hcl": code,
			})
			config, err := BuildDummyConfig("/", "", nil, nil)
			require.NoError(s.T(), err)
			_, err = RunDummyPlan(config)
			require.Error(s.T(), err)
			assert.Contains(s.T(), err.Error(), c.wantErr)
		})
	}
}

func (s *outputSuite) TestSensitiveOutputValueIsMarked() {
	c := s.buildConfig(`
output "secret" {
  value     = "top-secret"
  sensitive = true
}
`)
	_, err := RunDummyPlan(c)
	require.NoError(s.T(), err)

	output := s.onlyOutput(c)
	assert.True(s.T(), output.Sensitive())
	assert.True(s.T(), output.Value().HasMark(SensitiveMark))
	assert.Equal(s.T(), "<sensitive>", CtyValueToString(output.Value()))
}

func (s *outputSuite) TestSensitiveMarkPropagationPreservedWhenSensitiveNotDeclared() {
	s.dummyFsWithFiles(map[string]string{
		"test.hcl": `
marked_data "marked" source {
}

output "inherited" {
  value = marked_data.marked.source.value
}
`,
	})
	RegisterBlock(new(SensitiveMarkedDataBlock))
	c, err := BuildDummyConfig("/", "", nil, nil)
	require.NoError(s.T(), err)
	_, err = RunDummyPlan(c)
	require.NoError(s.T(), err)

	output := s.onlyOutput(c)
	assert.True(s.T(), output.Value().HasMarkDeep(SensitiveMark))
	assert.True(s.T(), output.Sensitive())
	// HCL flattens path-level marks to the root when traversing into an
	// object, so the nested mark surfaces as a root mark.
	assert.True(s.T(), output.Value().HasMark(SensitiveMark))
}

func (s *outputSuite) TestNestedSensitiveMarkIsPreserved() {
	s.dummyFsWithFiles(map[string]string{
		"test.hcl": `
marked_data "marked" source {
}

output "inherited" {
  value = marked_data.marked.source
}
`,
	})
	RegisterBlock(new(SensitiveMarkedDataBlock))
	c, err := BuildDummyConfig("/", "", nil, nil)
	require.NoError(s.T(), err)
	_, err = RunDummyPlan(c)
	require.NoError(s.T(), err)

	output := s.onlyOutput(c)
	assert.True(s.T(), output.Value().HasMarkDeep(SensitiveMark))
	assert.True(s.T(), output.Sensitive())
	assert.False(s.T(), output.Value().HasMark(SensitiveMark))
}

func (s *outputSuite) TestOutputBlockValueDefaultsToDynamicValBeforeEvaluation() {
	c := s.buildConfig(`
output "later" {
  value = "hello"
}
`)
	output := s.onlyOutput(c)
	assert.Equal(s.T(), cty.DynamicVal, output.Value())

	_, err := RunDummyPlan(c)
	require.NoError(s.T(), err)
	assert.True(s.T(), AreCtyValuesEqual(cty.StringVal("hello"), output.Value()))
}

func (s *outputSuite) TestOutputBlockAvailableInEvalContext() {
	c := s.buildConfig(`
output "first" {
  value = "hello"
}

output "second" {
  value = "${output.first} world"
}
`)
	_, err := RunDummyPlan(c)
	require.NoError(s.T(), err)

	outputs := Blocks[*OutputBlock](c)
	require.Len(s.T(), outputs, 2)
	byName := make(map[string]*OutputBlock)
	for _, output := range outputs {
		byName[output.Name()] = output
	}
	assert.True(s.T(), AreCtyValuesEqual(cty.StringVal("hello world"), byName["second"].Value()))
}

func (s *outputSuite) TestOutputBlockPrecondition() {
	cases := []struct {
		desc    string
		code    string
		wantErr string
	}{
		{
			desc: "passing precondition",
			code: `
variable "threshold" {
  default = 1
}

output "positive" {
  value = 1
  precondition {
    condition     = var.threshold > 0
    error_message = "must be positive"
  }
}
`,
		},
		{
			desc: "failing precondition",
			code: `
variable "threshold" {
  default = 0
}

output "positive" {
  value = 1
  precondition {
    condition     = var.threshold > 0
    error_message = "must be positive"
  }
}
`,
			wantErr: "must be positive",
		},
	}
	for _, c := range cases {
		s.Run(c.desc, func() {
			config := s.buildConfig(c.code)
			_, err := RunDummyPlan(config)
			if c.wantErr == "" {
				require.NoError(s.T(), err)
				return
			}
			require.Error(s.T(), err)
			assert.Contains(s.T(), err.Error(), c.wantErr)
		})
	}
}

func (s *outputSuite) TestOutputBlockDependsOn() {
	c := s.buildConfig(`
output "result" {
  value      = "hello"
  depends_on = [data.dummy.sample]
}

data "dummy" sample {
}
`)
	_, err := RunDummyPlan(c)
	require.NoError(s.T(), err)

	children, err := c.GetChildren("data.dummy.sample")
	require.NoError(s.T(), err)
	_, ok := children["output.result"]
	assert.True(s.T(), ok, "depends_on should order output after the data block")
}

func (s *outputSuite) TestOutputBlockRejectsUnknownAttributeAndNestedBlock() {
	cases := []struct {
		desc string
		code string
	}{
		{
			desc: "unknown attribute",
			code: `
output "result" {
  value = "hello"
  bogus = "nope"
}
`,
		},
		{
			desc: "unsupported nested block",
			code: `
output "result" {
  value = "hello"
  bogus_block {
  }
}
`,
		},
	}
	for _, c := range cases {
		s.Run(c.desc, func() {
			config := s.buildConfig(c.code)
			_, err := RunDummyPlan(config)
			require.Error(s.T(), err)
			assert.Contains(s.T(), err.Error(), "not expected")
		})
	}
}

func (s *outputSuite) TestValueExprString() {
	c := s.buildConfig(`
output "result" {
  value = 1 + 2
}
`)
	output := s.onlyOutput(c)
	assert.Equal(s.T(), "1 + 2", output.ValueExprString())
}

func (s *outputSuite) TestAsHclBlocksNormalizesOutputLabels() {
	content := []byte(`output "result" {
  value = "hello"
}`)
	readFile, diag := hclsyntax.ParseConfig(content, "test.hcl", hcl.InitialPos)
	require.False(s.T(), diag.HasErrors())
	writeFile, diag := hclwrite.ParseConfig(content, "test.hcl", hcl.InitialPos)
	require.False(s.T(), diag.HasErrors())

	blocks := AsHclBlocks(readFile.Body.(*hclsyntax.Body).Blocks, writeFile.Body().Blocks())
	require.Len(s.T(), blocks, 1)
	assert.Equal(s.T(), "output", blocks[0].Type)
	assert.Equal(s.T(), []string{"", "result"}, blocks[0].Labels)
}

func TestOutputBlockValueIsUnknownDuringPlanAndResolvedDuringApply(t *testing.T) {
	testBase := newTestBase()
	defer testBase.teardown()
	testBase.dummyFsWithFiles(map[string]string{
		"test.hcl": `
res_unknown "unknown" this {
}

output "resolved" {
  value = res_unknown.unknown.this.value
}
`,
	})

	RegisterBlock(new(UnknownApplyValueBlock))
	c, err := BuildDummyConfig("/", "", nil, nil)
	require.NoError(t, err)
	_, err = RunDummyPlan(c)
	require.NoError(t, err)

	outputs := Blocks[*OutputBlock](c)
	require.Len(t, outputs, 1)
	output := outputs[0]
	assert.False(t, output.Value().IsKnown(), "plan-time value should be unknown")
	assert.True(t, output.Value().Type().Equals(cty.String), "plan-time value should preserve its static type")

	require.NoError(t, c.RunApply(func(b Block) error {
		if applyBlock, ok := b.(ApplyBlock); ok {
			return applyBlock.Apply()
		}
		return nil
	}))

	assert.True(t, output.Value().IsKnown())
	assert.True(t, AreCtyValuesEqual(cty.StringVal("applied"), output.Value()))
}

func TestOutputBlockParallelDAG(t *testing.T) {
	testBase := newTestBase()
	defer testBase.teardown()
	testBase.dummyFsWithFiles(map[string]string{
		"test.hcl": `
output "one" {
  value = 1
}

output "two" {
  value = output.one + 1
}

output "three" {
  value = output.two + 1
}
`,
	})

	hclBlocks, err := loadHclBlocks(false, "")
	require.NoError(t, err)
	c := &parallelTestConfig{
		BaseConfig:  NewBasicConfig("/", "faketerraform", "ft", nil, nil, nil),
		parallelism: 4,
	}
	require.NoError(t, InitConfig(c, hclBlocks))
	require.NoError(t, c.RunPlan())

	outputs := Blocks[*OutputBlock](c)
	require.Len(t, outputs, 3)
	byName := make(map[string]cty.Value)
	for _, output := range outputs {
		byName[output.Name()] = output.Value()
	}
	assert.True(t, AreCtyValuesEqual(cty.NumberIntVal(1), byName["one"]))
	assert.True(t, AreCtyValuesEqual(cty.NumberIntVal(2), byName["two"]))
	assert.True(t, AreCtyValuesEqual(cty.NumberIntVal(3), byName["three"]))
}

var _ PlanBlock = &SensitiveMarkedDataBlock{}
var _ CustomDecode = &SensitiveMarkedDataBlock{}

// SensitiveMarkedDataBlock simulates a block producing a value already
// carrying SensitiveMark (e.g. a data source reading a secret).
type SensitiveMarkedDataBlock struct {
	*BaseBlock
}

func (m *SensitiveMarkedDataBlock) Decode(_ *HclBlock, _ *hcl.EvalContext) error {
	return nil
}

func (m *SensitiveMarkedDataBlock) Type() string {
	return "marked"
}

func (m *SensitiveMarkedDataBlock) BlockType() string {
	return "marked_data"
}

func (m *SensitiveMarkedDataBlock) AddressLength() int { return 3 }

func (m *SensitiveMarkedDataBlock) CanExecutePrePlan() bool { return false }

func (m *SensitiveMarkedDataBlock) ExecuteDuringPlan() error { return nil }

func (m *SensitiveMarkedDataBlock) BaseValues() map[string]cty.Value {
	values := m.BaseBlock.BaseValues()
	values["value"] = cty.StringVal("hidden").Mark(SensitiveMark)
	return values
}

var _ PlanBlock = &MarkedBoolDataBlock{}
var _ CustomDecode = &MarkedBoolDataBlock{}

// MarkedBoolDataBlock simulates a block producing a known boolean value
// carrying SensitiveMark.
type MarkedBoolDataBlock struct {
	*BaseBlock
}

func (m *MarkedBoolDataBlock) Decode(_ *HclBlock, _ *hcl.EvalContext) error {
	return nil
}

func (m *MarkedBoolDataBlock) Type() string {
	return "flag"
}

func (m *MarkedBoolDataBlock) BlockType() string {
	return "marked_bool"
}

func (m *MarkedBoolDataBlock) AddressLength() int { return 3 }

func (m *MarkedBoolDataBlock) CanExecutePrePlan() bool { return false }

func (m *MarkedBoolDataBlock) ExecuteDuringPlan() error { return nil }

func (m *MarkedBoolDataBlock) BaseValues() map[string]cty.Value {
	values := m.BaseBlock.BaseValues()
	values["value"] = cty.BoolVal(true).Mark(SensitiveMark)
	return values
}

var _ PlanBlock = &UnknownApplyValueBlock{}
var _ ApplyBlock = &UnknownApplyValueBlock{}
var _ CustomDecode = &UnknownApplyValueBlock{}

// UnknownApplyValueBlock simulates an ApplyBlock whose value is unknown at
// plan time and only resolves during apply.
type UnknownApplyValueBlock struct {
	*BaseBlock
	applied atomic.Bool
}

func (u *UnknownApplyValueBlock) Decode(_ *HclBlock, _ *hcl.EvalContext) error {
	return nil
}

func (u *UnknownApplyValueBlock) Type() string {
	return "unknown"
}

func (u *UnknownApplyValueBlock) BlockType() string {
	return "res_unknown"
}

func (u *UnknownApplyValueBlock) AddressLength() int { return 3 }

func (u *UnknownApplyValueBlock) CanExecutePrePlan() bool { return false }

func (u *UnknownApplyValueBlock) ExecuteDuringPlan() error { return nil }

func (u *UnknownApplyValueBlock) Apply() error {
	u.applied.Store(true)
	return nil
}

func (u *UnknownApplyValueBlock) BaseValues() map[string]cty.Value {
	values := u.BaseBlock.BaseValues()
	if u.applied.Load() {
		values["value"] = cty.StringVal("applied")
	} else {
		values["value"] = cty.UnknownVal(cty.String)
	}
	return values
}
