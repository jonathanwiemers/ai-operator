package chatcmd

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestParseTake(t *testing.T) {
	twenty := make([]string, 0, 21)
	for i := range 21 {
		twenty = append(twenty, "#"+strconv.Itoa(100+i))
	}
	for _, tc := range []struct {
		args    []string
		want    []int64
		wantErr string
	}{
		{[]string{"#350", "#421", "", ""}, []int64{350, 421}, ""},
		{[]string{"350,", "#421,#350", "  "}, []int64{350, 421}, ""},
		{[]string{"#7"}, []int64{7}, ""},
		{twenty[:20], nil, ""},
		{twenty, nil, "take needs 1 to 20 issues"},
		{[]string{"", ""}, nil, "take needs 1 to 20 issues"},
		{[]string{"#350", "please"}, nil, `"please" is not an issue #N`},
		{[]string{"#0"}, nil, `"#0" is not an issue #N`},
		{[]string{"%5"}, nil, `"%5" is not an issue #N`},
	} {
		got, err := ParseTake(tc.args)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: err = %v, want it to mention %q", tc.args, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.args, err)
			continue
		}
		if tc.want != nil && !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.args, got, tc.want)
		}
		if tc.want == nil && len(got) != 20 {
			t.Errorf("%q: got %d issues, want 20", tc.args, len(got))
		}
	}
}
