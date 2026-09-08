package golden

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHclFileParser_HeredocClosingMarkerAtEofWithoutTrailingNewline(t *testing.T) {
	cases := []struct {
		desc          string
		content       string
		expectedValue string
		expectedErr   string
	}{
		{
			desc:          "heredoc closing marker at EOF without trailing newline",
			content:       "topic = <<-TOPIC\nexample topic\nTOPIC",
			expectedValue: "example topic\n",
		},
		{
			desc:          "heredoc closing marker with trailing newline",
			content:       "topic = <<-TOPIC\nexample topic\nTOPIC\n",
			expectedValue: "example topic\n",
		},
		{
			desc:        "unterminated heredoc without closing marker",
			content:     "topic = <<-TOPIC\nexample topic",
			expectedErr: "Unterminated template string",
		},
		{
			desc:        "unterminated quoted string at EOF",
			content:     `topic = "hello`,
			expectedErr: "Invalid multi-line string",
		},
		{
			desc:        "unterminated heredoc consuming later attributes",
			content:     "topic = <<-TOPIC\nexample topic\nOTHER\ntopic = \"hello\"",
			expectedErr: "Unterminated template string",
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			sut := hclFileParser{}
			file, err := sut.ParseFile([]byte(c.content), "test.r42vars")
			if c.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), c.expectedErr)
				return
			}
			require.NoError(t, err)
			attrs, diag := file.Body.JustAttributes()
			require.False(t, diag.HasErrors())
			value, diag := attrs["topic"].Expr.Value(nil)
			require.False(t, diag.HasErrors())
			assert.Equal(t, c.expectedValue, value.AsString())
		})
	}
}
