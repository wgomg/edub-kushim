package handlers

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/tags"
)

func TestExtractReport(t *testing.T) {
	report := `{"run_id":"abc","rule_set_version":"v0","tags":3}`

	cases := []struct {
		name   string
		stdout string
		want   string
	}{
		{
			name:   "report line among the child's log lines",
			stdout: "<6>INFO  : tag measure starting\n" + tags.ReportPrefix + report + "\n<6>INFO  : done\n",
			want:   report,
		},
		{
			name:   "report without trailing newline",
			stdout: tags.ReportPrefix + report,
			want:   report,
		},
		{
			name:   "log output only",
			stdout: "<6>INFO  : tag measure starting\n",
			want:   "",
		},
		{
			name:   "marker with invalid json",
			stdout: tags.ReportPrefix + "not json\n",
			want:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractReport([]byte(tc.stdout))
			if tc.want == "" {
				if got != nil {
					t.Errorf("extractReport = %s, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("extractReport = nil, want the report payload")
			}
			if !json.Valid(got) || string(got) != tc.want {
				t.Errorf("extractReport = %s, want %s", got, tc.want)
			}
		})
	}
}
