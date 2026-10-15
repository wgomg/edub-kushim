package admission

import (
	"testing"

	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
)

func TestCompositionFeatures_ReadingRecord(t *testing.T) {
	parts := []string{"history", "china"}
	verdicts := map[string]PartVerdict{
		"history": {Dimensional: true},
		"china":   {Dimensional: true},
	}
	deferred := map[string]bool{}
	cv := CompoundVerdict{Status: analyzer.TagCompoundComposition}
	promoted := map[string]string{tags.Key("history"): "history", tags.Key("china"): "china"}

	firm := tags.NewReconstructibilityReading("history of china", parts, 10, 9, 9)
	far := tags.NewReconstructibilityReading("history of china", parts, 10, 9, 6)

	cases := []struct {
		name                 string
		coverComplete        bool
		reading              tags.ReconstructibilityReading
		promotedNames        map[string]string
		wantSource           string
		wantBand             string
		wantReconstructible  bool
		wantFar              bool
		wantPartsDimensional bool
	}{
		{"a sufficient firm reading answers from the substrate", true, firm, nil,
			tags.ReconstructibilitySourceSubstrate, "firm", true, false, true},
		{"a far reading answers from the substrate and denies reconstructibility", true, far, nil,
			tags.ReconstructibilitySourceSubstrate, "far", false, true, true},
		{"an unmeasurable cover falls back to the promoted-parts proxy", true, tags.ReconstructibilityReading{}, promoted,
			tags.ReconstructibilitySourceProxy, "", true, false, true},
		{"an unmeasurable cover over unpromoted parts still records the proxy", true, tags.ReconstructibilityReading{}, map[string]string{},
			tags.ReconstructibilitySourceProxy, "", false, false, true},
		{"no full cover records that the parts test never ran", false, tags.ReconstructibilityReading{}, nil,
			tags.ReconstructibilitySourceAbsent, "", false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, record := compositionFeatures(parts, tc.coverComplete, deferred, verdicts, cv, tc.promotedNames, tc.reading)

			if record.Source != tc.wantSource || record.Band != tc.wantBand {
				t.Errorf("record = {source:%q band:%q}, want {source:%q band:%q}",
					record.Source, record.Band, tc.wantSource, tc.wantBand)
			}
			if record.Source == tags.ReconstructibilitySourceSubstrate && record.Mass != tc.reading.CompoundMass {
				t.Errorf("record.Mass = %d, want the reading's %d", record.Mass, tc.reading.CompoundMass)
			}
			if f.Reconstructible != tc.wantReconstructible || f.FarFromIntersection != tc.wantFar || f.PartsDimensional != tc.wantPartsDimensional {
				t.Errorf("features = {reconstructible:%t far:%t partsDimensional:%t}, want {%t %t %t}",
					f.Reconstructible, f.FarFromIntersection, f.PartsDimensional,
					tc.wantReconstructible, tc.wantFar, tc.wantPartsDimensional)
			}
		})
	}
}
