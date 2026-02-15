package wiki

import (
	"reflect"
	"testing"
)

func TestExtractSecureBlockIDs(t *testing.T) {
	markdown := `
# Page
{{secure:ops_notes}}
Some text
{{ secure:infra }}
{{secure:ops_notes}}
`

	got := extractSecureBlockIDs(markdown)
	want := []string{"infra", "ops_notes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractSecureBlockIDs() = %#v, want %#v", got, want)
	}
}

func TestExtractSecureBlockIDsEmpty(t *testing.T) {
	got := extractSecureBlockIDs("# no secure macros here")
	if len(got) != 0 {
		t.Fatalf("expected empty block list, got %#v", got)
	}
}
