// Package identify implements line identification: given the observed
// peaks of ONE object (wavelengths only — no rest wavelengths, no prior
// redshift) and a snapshot of a rest-wavelength table, it finds every
// self-consistent bijection between peaks and table identities such that
// the per-line linear-law recession velocities agree within a fixed
// window. This package is pure: no HTTP, no persistence, no clock.
package identify

import (
	"math"
	"sort"
	"strconv"

	"cosmocalc/internal/calc"
	"cosmocalc/internal/lines"
)

// MaxVelocitySpreadKmS is the fixed tolerance: for a successful common
// identification, |v_i - v_j| must not exceed 200 km/s for any two peaks.
const MaxVelocitySpreadKmS = 200.0

// eps absorbs floating-point edge noise at window boundaries.
const eps = 1e-7

func spreadOf(pair []measured) float64 {
	minV, maxV := pair[0].v, pair[0].v
	for _, m := range pair[1:] {
		minV = math.Min(minV, m.v)
		maxV = math.Max(maxV, m.v)
	}
	return maxV - minV
}

// uniqueSorted drops adjacent duplicates from a sorted slice.
func uniqueSorted(xs []float64) []float64 {
	if len(xs) < 2 {
		return xs
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}

// assignmentKey identifies a bijection by the line assigned to each peak
// (peak order), so duplicate windows realising the same assignment merge.
func assignmentKey(pair []measured) string {
	// pair is stored in peak-index order; line indices are small ints.
	var b []byte
	for i, m := range pair {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, int64(m.lineIdx), 10)
	}
	return string(b)
}

// Status labels the outcome of an identification.
type Status string

const (
	// StatusUnique: exactly one cluster of consistent assignments.
	StatusUnique Status = "unique"
	// StatusAmbiguous: at least two clusters of consistent assignments
	// whose common redshifts differ by more than the velocity window.
	StatusAmbiguous Status = "ambiguous"
	// StatusNoMatch: no bijection makes all peaks share a velocity window.
	StatusNoMatch Status = "no_match"
)

// Peak is one observed peak of the target object.
type Peak struct {
	Wavelength float64 `json:"observed_wavelength"`
}

// PeakMatch is one peak's resolved identity within a candidate solution.
type PeakMatch struct {
	ObservedWavelength float64 `json:"observed_wavelength"`
	LineID             string  `json:"line_id"`
	LineLabel          string  `json:"line_label,omitempty"`
	RestWavelength     float64 `json:"rest_wavelength"`
	Redshift           float64 `json:"redshift"`
	VelocityKmS        float64 `json:"velocity_kms"`
}

// Candidate is one self-consistent identification of ALL peaks.
type Candidate struct {
	MeanRedshift    float64     `json:"redshift"`
	MeanVelocityKmS float64     `json:"velocity_kms"`
	ShiftType       string      `json:"shift_type"`
	MaxSpreadKmS    float64     `json:"max_velocity_spread_kms"`
	Matches         []PeakMatch `json:"matches"`
}

// Result is the complete outcome for one snapshot.
type Result struct {
	Status      Status
	Candidates  []Candidate
	WindowKmS   float64
	NumPeaks    int
	NumTableLns int
}

type measured struct {
	peakIdx  int
	lineIdx  int
	observed float64
	rest     float64
	z        float64
	v        float64
}

// Run performs identification strictly against the supplied snapshot.
// Callers are responsible for taking the snapshot before mutating the
// table and for validating inputs (units, positivity, emptiness); this
// function never reaches back into the registry.
//
// A solution is a bijection assigning every peak a DISTINCT table line so
// that max(v) - min(v) over the per-line velocities is at most W = 200
// km/s. It is found as follows: fix the first peak's assigned line a (an
// anchor at velocity v_a). Any valid window [L, L+W] containing v_a must
// have L in [v_a-W, v_a]; sliding L, its tightest distinct placements are
// at L = u-W for every distinct candidate velocity u that lies within
// [v_a-W, v_a+W]. Each such window is tested for a perfect bipartite
// matching (Kuhn) that places the remaining peaks on distinct lines whose
// velocities fall in the window; the realised assignment's actual spread
// is then verified <= W. Clustering by mean velocity then merges
// assignments that describe the same redshift cluster and separates
// genuinely ambiguous solutions.
func Run(snapshot *lines.Catalog, observed []float64) *Result {
	n := len(observed)
	res := &Result{
		Status:      StatusNoMatch,
		WindowKmS:   MaxVelocitySpreadKmS,
		NumPeaks:    n,
		NumTableLns: len(snapshot.Lines),
	}

	// adj[i] lists, for peak i, every (line, velocity) measurement.
	adj := make([][]measured, n)
	for i, obs := range observed {
		ms := make([]measured, 0, len(snapshot.Lines))
		for j, ln := range snapshot.Lines {
			z := obs/ln.RestWavelength - 1
			ms = append(ms, measured{
				peakIdx: i, lineIdx: j, observed: obs, rest: ln.RestWavelength,
				z: z, v: calc.SpeedOfLightKmS * z,
			})
		}
		adj[i] = ms
	}

	// Deduplicate candidate assignments by their ordered line-id tuple, so
	// different windows that realise the same bijection are counted once.
	seen := map[string]bool{}
	addCandidate := func(pair []measured) {
		key := assignmentKey(pair)
		if seen[key] {
			return
		}
		seen[key] = true
		res.Candidates = append(res.Candidates, buildCandidate(snapshot, pair))
	}

	// Anchor on the first peak; iterate its possible lines in deterministic
	// order (snapshot lines are sorted by id).
	for _, a := range adj[0] {
		// Every candidate velocity within 2W of the anchor can define a
		// tight window edge L = u-W that still contains v_a.
		edgeVelocities := make([]float64, 0)
		for j := 0; j < n; j++ {
			for _, m := range adj[j] {
				if m.v >= a.v-MaxVelocitySpreadKmS && m.v <= a.v+MaxVelocitySpreadKmS {
					edgeVelocities = append(edgeVelocities, m.v)
				}
			}
		}
		sort.Float64s(edgeVelocities)
		edgeVelocities = uniqueSorted(edgeVelocities)

		for _, u := range edgeVelocities {
			lo, hi := u-MaxVelocitySpreadKmS, u
			if a.v < lo-eps || a.v > hi+eps {
				continue // window must contain the anchor assignment
			}
			eligibility := make([][]int, n)
			ok := true
			for j := 1; j < n; j++ {
				ids := make([]int, 0)
				for _, m := range adj[j] {
					if m.v >= lo-eps && m.v <= hi+eps {
						ids = append(ids, m.lineIdx)
					}
				}
				if len(ids) == 0 {
					ok = false
					break
				}
				eligibility[j] = ids
			}
			if !ok {
				continue
			}
			pair := perfectMatching(snapshot, adj, eligibility, n, a)
			if pair == nil {
				continue
			}
			// The matching only proves lines fall in [lo, hi]; verify the
			// REALISED pairwise spread is within the window.
			if spreadOf(pair) > MaxVelocitySpreadKmS+eps {
				continue
			}
			addCandidate(pair)
		}
	}

	if len(res.Candidates) == 0 {
		return res
	}

	// Group candidate windows into redshift clusters greedily, walking them
	// in ascending mean velocity: a candidate starts a new cluster only when
	// it differs by more than the velocity window from every established
	// cluster anchor. Candidates whose common redshifts agree within the
	// window are the same solution; clusters separated by more than the
	// window are distinct ambiguous solutions.
	sort.SliceStable(res.Candidates, func(i, j int) bool {
		return res.Candidates[i].MeanVelocityKmS < res.Candidates[j].MeanVelocityKmS
	})
	type cluster struct {
		anchor Candidate
		best   Candidate
	}
	clusters := []cluster{}
	for _, c := range res.Candidates {
		idx := -1
		for k := range clusters {
			if math.Abs(c.MeanVelocityKmS-clusters[k].anchor.MeanVelocityKmS) <= MaxVelocitySpreadKmS {
				idx = k
				break
			}
		}
		if idx < 0 {
			clusters = append(clusters, cluster{anchor: c, best: c})
			continue
		}
		if tighter(c, clusters[idx].best) {
			clusters[idx].best = c
		}
	}

	reps := make([]Candidate, len(clusters))
	for i, cl := range clusters {
		reps[i] = cl.best // tightest representative of each cluster
	}
	res.Candidates = reps
	if len(clusters) > 1 {
		res.Status = StatusAmbiguous
		return res
	}
	res.Status = StatusUnique
	return res
}

// perfectMatching finds a bijection peaks->lines inside the window. Peak 0
// is fixed to anchor; peaks 1..n-1 are matched via Kuhn's algorithm. It
// returns the chosen measured assignment per peak (peak index order), or
// nil if no perfect matching exists.
func perfectMatching(snapshot *lines.Catalog, adj [][]measured, eligibility [][]int, n int, anchor measured) []measured {
	// For n == 1 the anchor alone is the complete assignment.
	if n == 1 {
		return []measured{anchor}
	}

	matchLine := map[int]int{} // lineIdx -> peak using it
	var aug func(peak int, seenLines map[int]bool) bool
	aug = func(peak int, seenLines map[int]bool) bool {
		for _, ln := range eligibility[peak] {
			if seenLines[ln] {
				continue
			}
			seenLines[ln] = true
			if owner, taken := matchLine[ln]; !taken || aug(owner, seenLines) {
				matchLine[ln] = peak
				return true
			}
		}
		return false
	}

	for p := 1; p < n; p++ {
		seen := map[int]bool{anchor.lineIdx: true}
		if !aug(p, seen) {
			return nil
		}
	}

	// Translate into a per-peak assignment and confirm bijection.
	chosen := make([]measured, n)
	chosen[0] = anchor
	used := map[int]bool{anchor.lineIdx: true}
	for ln, p := range matchLine {
		if used[ln] {
			return nil
		}
		used[ln] = true
		var found *measured
		for k := range adj[p] {
			if adj[p][k].lineIdx == ln {
				found = &adj[p][k]
				break
			}
		}
		if found == nil {
			return nil
		}
		chosen[p] = *found
	}
	for p := 1; p < n; p++ {
		if chosen[p].peakIdx != p {
			return nil
		}
	}
	return chosen
}

func buildCandidate(snapshot *lines.Catalog, pair []measured) Candidate {
	matches := make([]PeakMatch, len(pair))
	var sumZ, sumV, minV, maxV float64
	for i, m := range pair {
		ln := snapshot.Lines[m.lineIdx]
		matches[i] = PeakMatch{
			ObservedWavelength: m.observed,
			LineID:             ln.ID,
			LineLabel:          ln.Label,
			RestWavelength:     ln.RestWavelength,
			Redshift:           m.z,
			VelocityKmS:        m.v,
		}
		sumZ += m.z
		sumV += m.v
		if i == 0 {
			minV, maxV = m.v, m.v
		} else {
			minV = math.Min(minV, m.v)
			maxV = math.Max(maxV, m.v)
		}
	}
	// Matches are already in peak order; sort by observed wavelength
	// explicitly for stable output.
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].ObservedWavelength < matches[j].ObservedWavelength
	})
	meanZ := sumZ / float64(len(pair))
	meanV := sumV / float64(len(pair))
	return Candidate{
		MeanRedshift:    meanZ,
		MeanVelocityKmS: meanV,
		ShiftType:       string(shiftOf(meanZ)),
		MaxSpreadKmS:    maxV - minV,
		Matches:         matches,
	}
}

func shiftOf(z float64) calc.ShiftType {
	switch {
	case z > 0:
		return calc.ShiftRedshift
	case z < 0:
		return calc.ShiftBlueshift
	default:
		return calc.ShiftRest
	}
}

// tighter reports whether a is a preferable representative over b:
// smaller spread, then lexicographically smaller ordered line-id tuple.
func tighter(a, b Candidate) bool {
	if a.MaxSpreadKmS != b.MaxSpreadKmS {
		return a.MaxSpreadKmS < b.MaxSpreadKmS
	}
	for i := range a.Matches {
		if a.Matches[i].LineID != b.Matches[i].LineID {
			return a.Matches[i].LineID < b.Matches[i].LineID
		}
	}
	return false
}
