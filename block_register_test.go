package golden

import (
	"sync"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegisterBlockAsHclBlocksRace is a regression test for
// https://github.com/lonegunmanb/golden/issues/10: lazy block registration
// through RegisterBlock/RegisterBaseBlock used to race with the factory
// lookups performed by AsHclBlocks/readRawHclSyntaxBlock. Run with -race.
func TestRegisterBlockAsHclBlocksRace(t *testing.T) {
	parse := func() []*HclBlock {
		content := []byte(`resource "dummy" "test" {}`)
		readFile, diag := hclsyntax.ParseConfig(content, "test.hcl", hcl.InitialPos)
		require.False(t, diag.HasErrors(), diag.Error())
		writeFile, diag := hclwrite.ParseConfig(content, "test.hcl", hcl.InitialPos)
		require.False(t, diag.HasErrors(), diag.Error())
		return AsHclBlocks(readFile.Body.(*hclsyntax.Body).Blocks, writeFile.Body().Blocks())
	}

	const goroutines = 8
	const iterations = 100

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				RegisterBlock(new(DummyResource))
				RegisterBaseBlock(func() BlockType { return new(BaseResource) })
				assert.True(t, IsBlockTypeWanted("resource"))

				blocks := parse()
				require.Len(t, blocks, 1)

				constructor, ok := lookupBlockConstructor("resource", "dummy")
				require.True(t, ok)
				assert.NotNil(t, constructor)

				refIter, ok := lookupRefIterator("resource")
				require.True(t, ok)
				assert.NotNil(t, refIter)
			}
		}()
	}
	wg.Wait()
}
