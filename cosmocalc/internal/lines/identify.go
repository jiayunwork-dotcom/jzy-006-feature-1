// The matching engine: given observed peak wavelengths and a rest-line
// catalog (already snapshotted by the caller), enumerate every
// assignment of peaks to distinct catalog lines whose redshifts —
// converted to recession velocities with the default cosmological
// linear relation v = c·z — agree pairwise within VelocityWindowKmS.
package lines

import (
	"sort"
	"strconv"
	"strings"

	"cosmocalc/internal/calc"
)

// VelocityWindowKmS is the maximum allowed pairwise recession-velocity
// difference between the lines of one identification.
const VelocityWindowKmS = 200.0

// maxCandidates caps enumerated solutions; beyond it the outcome is
// ambiguous anyway and the caller is told the list was truncated.
const maxCandidates = 100

// Match pairs one observed peak with the catalog line it was
// identified as, including the redshift this pair implies on its own.
type Match struct {
	ObservedWavelength float64 `json:"observed_wavelength"`
	LineID             string  `json:"line_id"`
	RestWavelength     float64 `json:"rest_wavelength"`
	Redshift           float64 `json:"redshift"`
}

// Candidate is one self-consistent assignment of every peak to a
// distinct catalog line. CommonRedshift is the mean of the per-peak
// redshifts (which all lie within the velocity window of each other).
type Candidate struct {
	CommonRedshift float64 `json:"common_redshift"`
	Matches        []Match `json:"matches"`
}

// Identify enumerates all consistent peak→line assignments. It returns
// the candidates sorted by common redshift; truncated reports whether
// the enumeration hit maxCandidates. Zero candidates means no
// assignment exists; exactly one means a unique identification; more
// than one means the identification is ambiguous.
//
// The catalog is used exactly as given — the caller is responsible for
// passing the per-run snapshot, never a live table.
func Identify(peaks []float64, cat Catalog) (candidates []Candidate, truncated bool) {
	n := len(peaks)
	m := len(cat.Lines)
	if n == 0 || m == 0 {
		return nil, false
	}

	// z[j][i] is the redshift implied by pairing peak j with line i.
	z := make([][]float64, n)
	anchors := make([]float64, 0, n*m)
	for j := range peaks {
		z[j] = make([]float64, m)
		for i, ln := range cat.Lines {
			z[j][i] = peaks[j]/ln.RestWavelength - 1
			anchors = append(anchors, z[j][i])
		}
	}

	// The velocity window expressed in redshift: v = c·z is monotonic,
	// so pairwise |Δv| ≤ 200 km/s ⇔ (max z − min z) ≤ 200/c.
	window := VelocityWindowKmS / calc.SpeedOfLightKmS
	const eps = 1e-12

	seen := make(map[string]bool)
	var out []Candidate

	// Any feasible assignment's minimum redshift is one of the
	// candidate redshifts, so anchoring a window at every candidate
	// redshift discovers every feasible assignment.
	for _, anchor := range anchors {
		eligible := make([][]int, n)
		ok := true
		for j := 0; j < n; j++ {
			for i := 0; i < m; i++ {
				if z[j][i] >= anchor-eps && z[j][i] <= anchor+window+eps {
					eligible[j] = append(eligible[j], i)
				}
			}
			if len(eligible[j]) == 0 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}

		// Enumerate injective peak→line assignments within the window,
		// trying the most constrained peaks first.
		order := make([]int, n)
		for j := range order {
			order[j] = j
		}
		sort.Slice(order, func(a, b int) bool {
			return len(eligible[order[a]]) < len(eligible[order[b]])
		})
		assign := make([]int, n)
		used := make([]bool, m)
		var walk func(k int) bool // returns true to stop (cap reached)
		walk = func(k int) bool {
			if len(out) >= maxCandidates {
				return true
			}
			if k == n {
				key := assignmentKey(assign)
				if seen[key] {
					return false
				}
				seen[key] = true
				out = append(out, buildCandidate(peaks, cat.Lines, z, assign))
				return false
			}
			j := order[k]
			for _, i := range eligible[j] {
				if used[i] {
					continue
				}
				used[i] = true
				assign[j] = i
				if walk(k + 1) {
					return true
				}
				used[i] = false
			}
			return false
		}
		if walk(0) {
			truncated = true
			break
		}
	}

	sort.Slice(out, func(a, b int) bool { return out[a].CommonRedshift < out[b].CommonRedshift })
	return out, truncated
}

// assignmentKey renders an assignment (peak index → line index) as a
// deduplication key.
func assignmentKey(assign []int) string {
	var b strings.Builder
	for j, i := range assign {
		if j > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(i))
	}
	return b.String()
}

// buildCandidate materialises one assignment into a Candidate, keeping
// the matches in the caller's peak order.
func buildCandidate(peaks []float64, ls []Line, z [][]float64, assign []int) Candidate {
	matches := make([]Match, len(peaks))
	sum := 0.0
	for j := range peaks {
		i := assign[j]
		matches[j] = Match{
			ObservedWavelength: peaks[j],
			LineID:             ls[i].ID,
			RestWavelength:     ls[i].RestWavelength,
			Redshift:           z[j][i],
		}
		sum += z[j][i]
	}
	return Candidate{
		CommonRedshift: sum / float64(len(peaks)),
		Matches:        matches,
	}
}
