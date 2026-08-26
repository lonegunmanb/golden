package golden

import (
	"github.com/lonegunmanb/go-defaults"
	"reflect"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type blockConstructor = func(Config, *HclBlock) Block
type blockRegistry map[string]blockConstructor

// registryMu guards the package-level block registries below, which are
// mutated lazily by RegisterBlock/RegisterBaseBlock while other goroutines
// may be reading them through AsHclBlocks, wrapBlock or the dag walker.
var registryMu sync.RWMutex

var refIters = map[string]refIterator{}

var baseFactory = map[string]func() any{}
var blockSamples = map[string]Block{}

func RegisterBaseBlock(factory func() BlockType) {
	registryMu.Lock()
	defer registryMu.Unlock()
	bb := factory()
	baseFactory[bb.BlockType()] = func() any {
		return factory()
	}
}

func RegisterBlock(t Block) {
	bt := t.BlockType()
	refKeyWord := bt
	if s, ok := t.(BlockCustomizedRefType); ok {
		refKeyWord = s.CustomizedRefType()
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry, ok := factories[bt]
	if !ok {
		registry = make(blockRegistry)
		factories[bt] = registry
	}
	_, ok = refIters[refKeyWord]
	if !ok {
		refIters[refKeyWord] = iterator(refKeyWord, t.AddressLength())
	}
	blockSamples[bt] = t
	base, hasBase := baseFactory[bt]
	registry[t.Type()] = func(c Config, hb *HclBlock) Block {
		newBlock := reflect.New(reflect.TypeOf(t).Elem()).Elem()
		newBaseBlock := NewBaseBlock(c, hb)
		newBaseBlock.setForEach(hb.ForEach)
		newBaseBlock.setMetaNestedBlock()
		newBlock.FieldByName("BaseBlock").Set(reflect.ValueOf(newBaseBlock))
		b := newBlock.Addr().Interface().(Block)
		if hasBase {
			blockName := cases.Title(language.English).String(bt)
			newBlock.FieldByName("Base" + blockName).Set(reflect.ValueOf(base()))
		}
		defaults.SetDefaults(b)
		return b
	}
}

func IsBlockTypeWanted(bt string) bool {
	_, ok := lookupBlockSample(bt)
	return ok
}

func lookupBlockSample(bt string) (Block, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	block, ok := blockSamples[bt]
	return block, ok
}

func lookupBlockConstructor(blockType, blockName string) (blockConstructor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	registry, ok := factories[blockType]
	if !ok {
		return nil, false
	}
	constructor, ok := registry[blockName]
	return constructor, ok
}

func lookupRefIterator(name string) (refIterator, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	refIter, ok := refIters[name]
	return refIter, ok
}

func registerCommonBlock() {
	RegisterBlock(new(LocalBlock))
	RegisterBlock(new(VariableBlock))
}

var factories = map[string]blockRegistry{}
