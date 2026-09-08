package golden

import (
	"fmt"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"path/filepath"
)

type varFileParser interface {
	ParseFile(content []byte, fileName string) (*hcl.File, error)
}

var _ varFileParser = jsonFileParser{}

type jsonFileParser struct {
	dslAbbreviation string
}

func (j jsonFileParser) ParseFile(content []byte, fileName string) (*hcl.File, error) {
	if filepath.Ext(fileName) != ".json" {
		return nil, nil
	}
	parser := hclparse.NewParser()
	file, diag := parser.ParseJSON(content, fileName)
	if diag.HasErrors() {
		return nil, diag
	}
	return file, nil
}

var _ varFileParser = hclFileParser{}

type hclFileParser struct {
	dslAbbreviation string
}

func (h hclFileParser) ParseFile(content []byte, fileName string) (*hcl.File, error) {
	if filepath.Ext(fileName) == ".json" {
		return nil, nil
	}
	parser := hclparse.NewParser()
	file, diag := parser.ParseHCL(content, fileName)
	if diag.HasErrors() {
		// A heredoc whose closing marker is on the last line without a trailing
		// newline is reported as unterminated. Retry once with a trailing
		// newline so such files parse successfully.
		if !onlyUnterminatedTemplateAtEOF(diag, len(content)) {
			return nil, diag
		}
		// The parser caches files by name, so the retry requires a new parser.
		file, diag = hclparse.NewParser().ParseHCL(append(content, '\n'), fileName)
		if diag.HasErrors() {
			return nil, diag
		}
	}
	return file, nil
}

func onlyUnterminatedTemplateAtEOF(diag hcl.Diagnostics, contentLen int) bool {
	for _, d := range diag {
		if d.Severity != hcl.DiagError {
			return false
		}
		if d.Detail != "No closing marker was found for the string." {
			return false
		}
		if d.Subject == nil || d.Subject.End.Byte != contentLen {
			return false
		}
	}
	return true
}

var _ varFileParser = varFileParserImpl{}

type varFileParserImpl struct {
	dslAbbreviation string
}

func (h varFileParserImpl) ParseFile(content []byte, fileName string) (*hcl.File, error) {
	hclParser := hclFileParser{dslAbbreviation: h.dslAbbreviation} //nolint:gosimple,staticcheck
	file, err := hclParser.ParseFile(content, fileName)
	if file != nil || err != nil {
		return file, err
	}
	jsonParser := jsonFileParser{dslAbbreviation: h.dslAbbreviation} //nolint:gosimple,staticcheck
	file, err = jsonParser.ParseFile(content, fileName)
	if file != nil || err != nil {
		return file, err
	}
	return nil, fmt.Errorf("incorrect file %s: %+v", fileName, err)
}
