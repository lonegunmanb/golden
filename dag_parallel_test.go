package golden

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

type parallelTestConfig struct {
	*BaseConfig
	parallelism int
}

func (c *parallelTestConfig) Parallelism() int {
	return c.parallelism
}

func newParallelTestConfig(d *Dag, parallelism int) *parallelTestConfig {
	c := &parallelTestConfig{
		BaseConfig:  NewBasicConfig("", "test", "test", nil, nil, nil),
		parallelism: parallelism,
	}
	c.d = d
	c.setParallelism(&parallelism)
	return c
}

func newParallelTestBlock(address string, ready bool) *DummyResource {
	b := &DummyResource{
		BaseBlock: &BaseBlock{
			blockAddress: address,
			hb: &HclBlock{Block: &hclsyntax.Block{
				Body: &hclsyntax.Body{Attributes: hclsyntax.Attributes{}},
			}},
		},
	}
	if ready {
		b.markReady()
	}
	return b
}

func addParallelTestBlock(t *testing.T, d *Dag, address string, ready bool) *DummyResource {
	t.Helper()
	b := newParallelTestBlock(address, ready)
	require.NoError(t, d.AddVertexByID(address, b))
	return b
}

func waitForParallelCallbacks(t *testing.T, started <-chan string, count int) {
	t.Helper()
	for range count {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %d parallel callbacks", count)
		}
	}
}

type trackingPrePlanBlock struct {
	*BaseBlock
	started chan<- string
	release <-chan struct{}
}

func (b *trackingPrePlanBlock) Type() string {
	return "tracking"
}

func (b *trackingPrePlanBlock) BlockType() string {
	return "tracking"
}

func (b *trackingPrePlanBlock) AddressLength() int {
	return 1
}

func (b *trackingPrePlanBlock) CanExecutePrePlan() bool {
	return true
}

func (b *trackingPrePlanBlock) ExecuteBeforePlan() error {
	b.started <- b.Address()
	<-b.release
	return nil
}

func newTrackingPrePlanBlock(address string, started chan<- string, release <-chan struct{}) *trackingPrePlanBlock {
	return &trackingPrePlanBlock{
		BaseBlock: &BaseBlock{
			blockAddress: address,
			hb: &HclBlock{Block: &hclsyntax.Block{
				Body: &hclsyntax.Body{Attributes: hclsyntax.Attributes{}},
			}},
		},
		started: started,
		release: release,
	}
}

func TestInitConfigCachesParallelism(t *testing.T) {
	c := &parallelTestConfig{
		BaseConfig:  NewBasicConfig("", "test", "test", nil, nil, nil),
		parallelism: 3,
	}

	require.NoError(t, InitConfig(c, nil))
	require.NotNil(t, c.BaseConfig.parallelism)
	assert.Equal(t, 3, *c.BaseConfig.parallelism)

	c.parallelism = 1
	assert.Equal(t, 3, *c.BaseConfig.parallelism)
}

func TestInitConfigLeavesParallelismUnsetWhenUnsupported(t *testing.T) {
	baseConfig := NewBasicConfig("", "test", "test", nil, nil, nil)
	staleParallelism := 3
	baseConfig.setParallelism(&staleParallelism)
	c := &DummyConfig{BaseConfig: baseConfig}

	require.NoError(t, InitConfig(c, nil))
	assert.Nil(t, c.parallelism)
}

func TestRunPrePlanOnParallelConfigRemainsSerial(t *testing.T) {
	d := newDag()
	started := make(chan string, 2)
	release := make(chan struct{})
	for _, address := range []string{"var.first", "var.second"} {
		block := newTrackingPrePlanBlock(address, started, release)
		require.NoError(t, d.AddVertexByID(address, block))
	}

	c := newParallelTestConfig(d, 2)
	done := make(chan error, 1)
	go func() {
		done <- c.RunPrePlan()
	}()

	waitForParallelCallbacks(t, started, 1)
	select {
	case address := <-started:
		t.Fatalf("pre-plan callback %s ran concurrently", address)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-done)
	assert.Len(t, started, 1)
}

func TestInitConfigWithParallelismSupportsCliObjectVariables(t *testing.T) {
	testBase := newTestBase()
	defer testBase.teardown()
	testBase.dummyFsWithFiles(map[string]string{
		"test.hcl": `
variable "target" {
  type    = string
  default = "hello"
}

variable "audience" {
  type    = string
  default = "world"
}

variable "model_provider" {
  type = object({
    api_key_ref = string
    region      = optional(string, "us-east-1")
  })
}
`,
	})

	for range 20 {
		hclBlocks, err := loadHclBlocks(false, "")
		require.NoError(t, err)

		c := &parallelTestConfig{
			BaseConfig: NewBasicConfig("/", "faketerraform", "ft", nil, []CliFlagAssignedVariables{
				NewCliFlagAssignedVariable("model_provider", `{
  api_key_ref = "provider-key"
}`),
			}, nil),
			parallelism: 3,
		}
		require.NoError(t, InitConfig(c, hclBlocks))

		variables := make(map[string]*VariableBlock)
		for _, variable := range Blocks[*VariableBlock](c) {
			variables[variable.Name()] = variable
		}
		require.Equal(t, cty.StringVal("hello"), *variables["target"].variableValue)
		require.Equal(t, cty.StringVal("world"), *variables["audience"].variableValue)
		require.Equal(t, cty.ObjectVal(map[string]cty.Value{
			"api_key_ref": cty.StringVal("provider-key"),
			"region":      cty.StringVal("us-east-1"),
		}), *variables["model_provider"].variableValue)
	}
}

func TestRunApplyOnParallelHonorsParallelism(t *testing.T) {
	d := newDag()
	const blockCount = 6
	const parallelism = 2
	for i := range blockCount {
		addParallelTestBlock(t, d, fmt.Sprintf("block.%d", i), true)
	}

	c := newParallelTestConfig(d, parallelism)
	started := make(chan string, blockCount)
	release := make(chan struct{})
	done := make(chan error, 1)
	var current atomic.Int32
	var maximum atomic.Int32

	go func() {
		done <- c.RunApply(func(b Block) error {
			running := current.Add(1)
			for {
				observed := maximum.Load()
				if running <= observed || maximum.CompareAndSwap(observed, running) {
					break
				}
			}
			started <- b.Address()
			<-release
			current.Add(-1)
			return nil
		})
	}()

	waitForParallelCallbacks(t, started, parallelism)
	assert.Equal(t, int32(parallelism), maximum.Load())
	select {
	case address := <-started:
		t.Fatalf("callback %s exceeded parallelism limit", address)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, int32(parallelism), maximum.Load())
	assert.Len(t, started, blockCount-parallelism)
}

func TestRunApplyOnParallelWaitsForDependenciesFromCurrentRun(t *testing.T) {
	d := newDag()
	for _, address := range []string{"A", "B", "C", "D"} {
		// Apply starts with blocks already marked ready by Plan.
		addParallelTestBlock(t, d, address, true)
	}
	require.NoError(t, d.AddEdge("A", "C"))
	require.NoError(t, d.AddEdge("B", "C"))
	require.NoError(t, d.AddEdge("C", "D"))

	c := newParallelTestConfig(d, 2)
	rootStarted := make(chan string, 2)
	releaseRoots := make(chan struct{})
	done := make(chan error, 1)
	var mu sync.Mutex
	completed := make(map[string]bool)

	go func() {
		done <- c.RunApply(func(b Block) error {
			address := b.Address()
			if address == "A" || address == "B" {
				rootStarted <- address
				<-releaseRoots
			}

			mu.Lock()
			defer mu.Unlock()
			switch address {
			case "C":
				if !completed["A"] || !completed["B"] {
					return errors.New("C started before both parents completed")
				}
			case "D":
				if !completed["C"] {
					return errors.New("D started before C completed")
				}
			}
			completed[address] = true
			return nil
		})
	}()

	waitForParallelCallbacks(t, rootStarted, 2)
	mu.Lock()
	assert.Empty(t, completed)
	mu.Unlock()
	close(releaseRoots)
	require.NoError(t, <-done)
	assert.Equal(t, map[string]bool{"A": true, "B": true, "C": true, "D": true}, completed)
}

func TestRunApplyOnParallelBlocksFailedDescendantsAndContinuesIndependentBranches(t *testing.T) {
	d := newDag()
	for _, address := range []string{"failed", "blocked", "independent", "independent-child"} {
		addParallelTestBlock(t, d, address, true)
	}
	require.NoError(t, d.AddEdge("failed", "blocked"))
	require.NoError(t, d.AddEdge("independent", "independent-child"))

	c := newParallelTestConfig(d, 2)
	var called sync.Map
	err := c.RunApply(func(b Block) error {
		called.Store(b.Address(), true)
		if b.Address() == "failed" {
			return errors.New("apply failed")
		}
		return nil
	})

	require.ErrorContains(t, err, "apply failed")
	_, blockedCalled := called.Load("blocked")
	assert.False(t, blockedCalled)
	_, independentCalled := called.Load("independent")
	assert.True(t, independentCalled)
	_, independentChildCalled := called.Load("independent-child")
	assert.True(t, independentChildCalled)
}

func TestRunApplyOnParallelRejectsNonPositiveParallelism(t *testing.T) {
	d := newDag()
	addParallelTestBlock(t, d, "block", true)
	c := newParallelTestConfig(d, 0)

	err := c.RunApply(func(Block) error {
		t.Fatal("callback should not run")
		return nil
	})

	require.EqualError(t, err, "parallelism must be greater than zero, got 0")
}

func TestRunApplyWithoutParallelismRemainsSerial(t *testing.T) {
	d := newDag()
	for _, address := range []string{"A", "B", "C"} {
		addParallelTestBlock(t, d, address, true)
	}
	c := &DummyConfig{BaseConfig: NewBasicConfig("", "test", "test", nil, nil, nil)}
	c.d = d

	var current atomic.Int32
	var maximum atomic.Int32
	require.NoError(t, c.RunApply(func(Block) error {
		running := current.Add(1)
		defer current.Add(-1)
		for {
			observed := maximum.Load()
			if running <= observed || maximum.CompareAndSwap(observed, running) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		return nil
	}))

	assert.Equal(t, int32(1), maximum.Load())
}

func TestRunApplyCanSkipUnselectedBlocks(t *testing.T) {
	d := newDag()
	for _, address := range []string{"unselected", "selected"} {
		addParallelTestBlock(t, d, address, true)
	}
	require.NoError(t, d.AddEdge("unselected", "selected"))
	c := newParallelTestConfig(d, 2)

	var applied []string
	require.NoError(t, c.RunApply(func(b Block) error {
		if b.Address() == "selected" {
			applied = append(applied, b.Address())
		}
		return nil
	}))

	assert.Equal(t, []string{"selected"}, applied)
}

func TestRunApplyCanRedecodeUsingCompletedUpstreamValues(t *testing.T) {
	testBase := newTestBase()
	defer testBase.teardown()
	testBase.dummyFsWithFiles(map[string]string{
		"test.hcl": `
resource "dummy" upstream {
  tags = {
    value = "planned"
  }
}

resource "dummy" downstream {
  tags = resource.dummy.upstream.tags
}
`,
	})

	hclBlocks, err := loadHclBlocks(false, "")
	require.NoError(t, err)
	c := &parallelTestConfig{
		BaseConfig:  NewBasicConfig("", "faketerraform", "ft", nil, nil, nil),
		parallelism: 2,
	}
	require.NoError(t, InitConfig(c, hclBlocks))
	require.NoError(t, c.RunPlan())

	resources := Blocks[*DummyResource](c)
	require.Len(t, resources, 2)
	resourcesByName := make(map[string]*DummyResource, len(resources))
	for _, resource := range resources {
		resourcesByName[resource.Name()] = resource
	}

	require.NoError(t, c.RunApply(func(b Block) error {
		resource, ok := b.(*DummyResource)
		if !ok {
			return nil
		}
		if resource.Name() == "upstream" {
			resource.Tags = map[string]string{"value": "applied"}
			return nil
		}
		return Decode(resource)
	}))

	assert.Equal(t, map[string]string{"value": "applied"}, resourcesByName["downstream"].Tags)
}

// gatedSingleValue is a ready-for-read SingleValueBlock whose Value reports
// when a read starts, then blocks until it is released, so a parallel decode
// of the same block is forced to overlap the read.
type gatedSingleValue struct {
	*BaseBlock
	Tags map[string]string `json:"tags" hcl:"tags,optional"`

	gateOnce    sync.Once
	readStarted chan struct{}
	readRelease <-chan struct{}
}

func (b *gatedSingleValue) Type() string {
	return "gated"
}

func (b *gatedSingleValue) BlockType() string {
	return "gated"
}

func (b *gatedSingleValue) AddressLength() int {
	return 1
}

func (b *gatedSingleValue) CanExecutePrePlan() bool {
	return false
}

func (b *gatedSingleValue) Decode(hb *HclBlock, ctx *hcl.EvalContext) error {
	// Emulate the field rewrite that Decode performs, so a concurrent Value
	// read through a parallel local's eval context overlaps the write.
	b.Tags = map[string]string{"value": "decoded"}
	return nil
}

func (b *gatedSingleValue) Value() cty.Value {
	b.rlockValue()
	defer b.runlockValue()
	// Only the first read is gated; gateOnce keeps the channel close and the
	// field writes race-free when the eval context is built concurrently.
	b.gateOnce.Do(func() {
		close(b.readStarted)
		<-b.readRelease
	})
	return cty.ObjectVal(map[string]cty.Value{
		"tags": ToCtyValue(b.Tags),
	})
}

func TestParallelConfigRunPlanDecodeDoesNotRaceWithValueReads(t *testing.T) {
	// Regression test for the race between Decode's field rewrite on a block
	// and SingleValueBlock.Value reads through SingleValues while the DAG
	// plans in parallel. The gated block is already marked ready (as the
	// issue's local.source was), so a parallel local's evaluation reads its
	// value through SingleValues for the eval context, while a concurrent
	// dagPlan decodes the same block (its custom Decode rewrites its fields).
	// The per-block lock must serialize the decode's writes with the
	// eval-context reads; otherwise the race detector reports the overlap.
	testBase := newTestBase()
	defer testBase.teardown()
	testBase.dummyFsWithFiles(map[string]string{
		"test.hcl": `
locals {
  a = "world"
}
`,
	})

	hclBlocks, err := loadHclBlocks(false, "")
	require.NoError(t, err)
	c := &parallelTestConfig{
		BaseConfig:  NewBasicConfig("/", "faketerraform", "ft", nil, nil, nil),
		parallelism: 2,
	}
	require.NoError(t, InitConfig(c, hclBlocks))

	readStarted := make(chan struct{})
	readRelease := make(chan struct{})
	gatedBlock := &gatedSingleValue{
		BaseBlock:   NewBaseBlock(c, nil),
		Tags:        map[string]string{"value": "hello"},
		readStarted: readStarted,
		readRelease: readRelease,
	}
	// Mark the block ready and add it to the DAG so the parallel local's eval
	// context reads it via SingleValues, mirroring the issue's local.source.
	gatedBlock.markReady()
	require.NoError(t, c.d.AddVertexByID(gatedBlock.Address(), gatedBlock))

	planErr := make(chan error, 1)
	go func() {
		planErr <- c.RunPlan()
	}()

	// Wait until the local's evaluation is reading the gated block's value,
	// then decode the same block concurrently (as dagPlan does on the other
	// ready branch). With the per-block lock, the decode waits for the read
	// and both are serialized; without it, the decode's write overlaps the
	// read and the race detector reports it.
	select {
	case <-readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the value read to start")
	}
	decodeDone := make(chan error, 1)
	go func() {
		decodeDone <- Decode(gatedBlock)
	}()
	// Give the decode a chance to overlap the in-flight read before
	// releasing the read.
	time.Sleep(100 * time.Millisecond)
	close(readRelease)
	require.NoError(t, <-decodeDone)
	require.NoError(t, <-planErr)

	assert.Equal(t, map[string]string{"value": "decoded"}, gatedBlock.Tags)
}

func TestParallelConfigRunPlanSupportsForEachDependencies(t *testing.T) {
	testBase := newTestBase()
	defer testBase.teardown()
	testBase.dummyFsWithFiles(map[string]string{
		"test.hcl": `
data "dummy" source {
  data = {
    one = "first"
    two = "second"
  }
}

resource "dummy" expanded {
  for_each = data.dummy.source.data
  tags = {
    value = each.value
  }
}

resource "dummy" downstream {
  tags = resource.dummy.expanded["one"].tags
}
`,
	})

	hclBlocks, err := loadHclBlocks(false, "")
	require.NoError(t, err)
	c := &parallelTestConfig{
		BaseConfig:  NewBasicConfig("", "faketerraform", "ft", nil, nil, nil),
		parallelism: 4,
	}
	require.NoError(t, InitConfig(c, hclBlocks))
	require.NotNil(t, c.BaseConfig.parallelism)
	require.Equal(t, 4, *c.BaseConfig.parallelism)
	require.NoError(t, c.RunPlan())

	resources := Blocks[TestResource](c)
	require.Len(t, resources, 3)
	var downstream *DummyResource
	for _, resource := range resources {
		if resource.Name() == "downstream" {
			downstream = resource.(*DummyResource)
			break
		}
	}
	require.NotNil(t, downstream)
	assert.Equal(t, map[string]string{"value": "first"}, downstream.Tags)
}
