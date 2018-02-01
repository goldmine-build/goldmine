package data

import (
	"sort"

	"go.skia.org/infra/fuzzer/go/common"
)

type FuzzReport struct {
	// These are set by fuzzpool on ingestion.
	FileName     string `json:"fileName"`
	FunctionName string `json:"functionName"`
	LineNumber   int    `json:"lineNumber"`

	Stacktraces map[string]StackTrace `json:"stacktraces"`
	Flags       map[string][]string   `json:"flags"`

	FuzzName         string `json:"fuzzName"`
	FuzzCategory     string `json:"category"`
	FuzzArchitecture string `json:"architecture"`
	IsGrey           bool   `json:"isGrey"`
}

// ParseReport creates a report given the raw materials passed in.
func ParseReport(g GCSPackage) FuzzReport {
	result := ParseGCSPackage(g)

	stacktraces := map[string]StackTrace{}
	flags := map[string][]string{}
	for _, t := range common.ANALYSIS_TYPES {
		stacktraces[t] = result.Configs[t].StackTrace
		flags[t] = result.Configs[t].Flags.ToHumanReadableFlags()
	}

	return FuzzReport{
		Stacktraces:      stacktraces,
		Flags:            flags,
		FuzzName:         g.Name,
		FuzzCategory:     g.FuzzCategory,
		FuzzArchitecture: g.FuzzArchitecture,
		IsGrey:           result.IsGrey(),
	}
}

// SortedFuzzReports keeps the fuzzes sorted by FuzzName, i.e. the hash of the contents.
type SortedFuzzReports []FuzzReport

func (p SortedFuzzReports) Len() int           { return len(p) }
func (p SortedFuzzReports) Less(i, j int) bool { return p[i].FuzzName < p[j].FuzzName }
func (p SortedFuzzReports) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// Append adds b to the already sorted caller, and returns the sorted result. If a fuzz
// with the same FuzzName already exists, b replaces it.
// Precondition: Caller must be nil or sorted
func (p SortedFuzzReports) Append(b FuzzReport) SortedFuzzReports {
	i := sort.Search(len(p), func(j int) bool {
		return p[j].FuzzName >= b.FuzzName
	})
	if i >= len(p) {
		return append(p, b)
	}
	if p[i].FuzzName != b.FuzzName {
		// insert
		p = append(p, b)
		// shift all elements over 1
		copy(p[i+1:], p[i:])
		// put b in the correct index
		p[i] = b
	}
	// replace
	p[i] = b
	return p
}

// containsName returns the FuzzReport and true if a fuzz with the given name is in the list.
func (p SortedFuzzReports) containsName(fuzzName string) (FuzzReport, bool) {
	i := sort.Search(len(p), func(i int) bool { return p[i].FuzzName >= fuzzName })
	if i < len(p) && p[i].FuzzName == fuzzName {
		return p[i], true
	}
	return FuzzReport{}, false
}
