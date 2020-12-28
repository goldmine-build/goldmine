// Package databuilder provides a tool for generating test data in a way that is easy for
// a human to update and understand.
package databuilder

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"strings"
	"time"

	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/golden/go/sql"
	"go.skia.org/infra/golden/go/sql/schema"
	"go.skia.org/infra/golden/go/types"
)

// SQLDataBuilder has methods on it for generating trace data and other related data in a way
// that can be easily turned into SQL table rows.
type SQLDataBuilder struct {
	commitBuilder   *CommitBuilder
	groupingKeys    []string
	symbolsToDigest map[rune]schema.DigestBytes
	traceBuilders   []*TraceBuilder
}

// Commits returns a new CommitBuilder linked to this builder which will have a set. It panics if
// called more than once.
func (b *SQLDataBuilder) Commits() *CommitBuilder {
	if b.commitBuilder != nil {
		logAndPanic("Cannot call Commits() more than once.")
	}
	b.commitBuilder = &CommitBuilder{}
	return b.commitBuilder
}

// UseDigests loads a mapping of symbols (runes) to the digest that they represent. This allows
// specifying the trace history be done with a string of characters. If a rune is invalid or
// the digests are invalid, this will panic. It panics if called more than once.
func (b *SQLDataBuilder) UseDigests(symbolsToDigest map[rune]types.Digest) {
	if b.symbolsToDigest != nil {
		logAndPanic("Cannot call UseDigests() more than once.")
	}
	m := make(map[rune]schema.DigestBytes, len(symbolsToDigest))
	for symbol, digest := range symbolsToDigest {
		if symbol == '-' {
			logAndPanic("Cannot map something to -")
		}
		d, err := sql.DigestToBytes(digest)
		if err != nil {
			logAndPanic("Invalid digest %q: %s", digest, err)
		}
		m[symbol] = d
	}
	b.symbolsToDigest = m
}

// SetGroupingKeys specifies which keys from a Trace's params will be used to define the grouping.
// It panics if called more than once.
func (b *SQLDataBuilder) SetGroupingKeys(fields ...string) {
	if b.groupingKeys != nil {
		logAndPanic("Cannot call SetGroupingKeys() more than once.")
	}
	b.groupingKeys = fields
}

// TracesWithCommonKeys returns a new TraceBuilder for building a set of related traces. This can
// be called more than once - all data will be combined at the end. It panics if any of its
// prerequisites have not been called.
func (b *SQLDataBuilder) TracesWithCommonKeys(params paramtools.Params) *TraceBuilder {
	if b.commitBuilder == nil {
		logAndPanic("Must add commits before traces")
	}
	if len(b.commitBuilder.commits) == 0 {
		logAndPanic("Must specify at least one commit")
	}
	if len(b.groupingKeys) == 0 {
		logAndPanic("Must add grouping keys before traces")
	}
	if len(b.symbolsToDigest) == 0 {
		logAndPanic("Must add digests before traces")
	}
	tb := &TraceBuilder{
		commits:         b.commitBuilder.commits,
		commonKeys:      params,
		symbolsToDigest: b.symbolsToDigest,
		groupingKeys:    b.groupingKeys,
	}
	b.traceBuilders = append(b.traceBuilders, tb)
	return tb
}

// GenerateStructs should be called when all the data has been loaded in for a given setup and
// it will generate the SQL rows as represented in a schema.Tables. If any validation steps fail,
// it will panic.
func (b *SQLDataBuilder) GenerateStructs() schema.Tables {
	var rv schema.Tables
	commitsWithData := map[schema.CommitID]bool{}
	for _, builder := range b.traceBuilders {
		// Add unique rows from the tables gathered by tracebuilders.
	nextOption:
		for _, opt := range builder.options {
			for _, existingOpt := range rv.Options {
				if bytes.Equal(opt.OptionsID, existingOpt.OptionsID) {
					continue nextOption
				}
			}
			rv.Options = append(rv.Options, opt)
		}
	nextGrouping:
		for _, g := range builder.groupings {
			for _, existingG := range rv.Groupings {
				if bytes.Equal(g.GroupingID, existingG.GroupingID) {
					continue nextGrouping
				}
			}
			rv.Groupings = append(rv.Groupings, g)
		}
	nextSource:
		for _, sf := range builder.sourceFiles {
			for _, existingSF := range rv.SourceFiles {
				if bytes.Equal(sf.SourceFileID, existingSF.SourceFileID) {
					continue nextSource
				}
			}
			rv.SourceFiles = append(rv.SourceFiles, sf)
		}
		for _, t := range builder.traces {
			for _, existingT := range rv.Traces {
				if bytes.Equal(t.TraceID, existingT.TraceID) {
					// Having a duplicate trace means that there are duplicate TraceValues entries
					// and that is not intended.
					logAndPanic("Duplicate trace found: %v", t.Keys)
				}
			}
			rv.Traces = append(rv.Traces, t)
		}
		for _, xtv := range builder.traceValues {
			for _, tv := range xtv {
				if tv != nil {
					if tv.TraceID == nil || tv.GroupingID == nil {
						panic("Incomplete data - you must call Keys()")
					}
					if tv.OptionsID == nil {
						panic("Incomplete data - you must call Options*()")
					}
					if tv.SourceFileID == nil {
						panic("Incomplete data - you must call IngestedFrom()")
					}
					rv.TraceValues = append(rv.TraceValues, *tv)
					commitsWithData[tv.CommitID] = true
				}
			}
		}
	}
	rv.Commits = b.commitBuilder.commits
	for i := range rv.Commits {
		cid := rv.Commits[i].CommitID
		if commitsWithData[cid] {
			rv.Commits[i].HasData = true
		}
	}
	return rv
}

// CommitBuilder has methods for easily building commit history. All methods are chainable.
type CommitBuilder struct {
	commits    []schema.CommitRow
	previousID int
}

// Append adds a commit whose ID is one higher than the previous commits ID. It panics if
// the commitTime is not formatted to RFC3339.
func (b *CommitBuilder) Append(author, subject, commitTime string) *CommitBuilder {
	commitID := b.previousID + 1
	gitHash := fmt.Sprintf("%04d", commitID)
	// A true githash is 40 hex characters, so we repeat the 4 digits of the commitID 10 times.
	gitHash = strings.Repeat(gitHash, 10)
	ct, err := time.Parse(time.RFC3339, commitTime)
	if err != nil {
		logAndPanic("Invalid time %q: %s", commitTime, err)
	}
	b.commits = append(b.commits, schema.CommitRow{
		CommitID:    schema.CommitID(commitID),
		GitHash:     gitHash,
		CommitTime:  ct,
		AuthorEmail: author,
		Subject:     subject,
		HasData:     false,
	})
	b.previousID = commitID
	return b
}

// TraceBuilder has methods for easily building trace data. All methods are chainable.
type TraceBuilder struct {
	// inputs needed upon creation
	commits         []schema.CommitRow
	commonKeys      paramtools.Params
	groupingKeys    []string
	symbolsToDigest map[rune]schema.DigestBytes

	// built as a result of the calling methods
	groupings   []schema.GroupingRow
	options     []schema.OptionsRow
	sourceFiles []schema.SourceFileRow
	traceValues [][]*schema.TraceValueRow // each row is one trace's data points
	traces      []schema.TraceRow
}

// History takes in a slice of strings, with each string representing the history of a trace. Each
// string must have a number of symbols equal to the length of the number of commits. A dash '-'
// means no data at that commit; any other symbol must match the previous call to UseDigests().
// If any data is invalid or missing, this method panics.
func (b *TraceBuilder) History(traceHistories []string) *TraceBuilder {
	if len(b.traceValues) > 0 {
		logAndPanic("History must be called only once.")
	}
	// traceValues will have length len(commits) * numTraces after this is complete. Some entries
	// may be nil to represent "no data" and will be stripped out later.
	for _, th := range traceHistories {
		if len(th) != len(b.commits) {
			logAndPanic("history %q is of invalid length: expected %d", th, len(b.commits))
		}
		traceValues := make([]*schema.TraceValueRow, len(b.commits))
		for i, symbol := range th {
			if symbol == '-' {
				continue
			}
			digest, ok := b.symbolsToDigest[symbol]
			if !ok {
				logAndPanic("Unknown symbol in trace history %s", string(symbol))
			}
			traceValues[i] = &schema.TraceValueRow{
				CommitID: b.commits[i].CommitID,
				Digest:   digest,
			}
		}
		b.traceValues = append(b.traceValues, traceValues)
	}
	return b
}

// Keys specifies the params for each trace. It must be called after History() and the keys param
// must have the same number of elements that the call to History() had. The nth element here
// represents the nth trace history. This method panics if any trace would end up being identical
// or lacks the grouping data. This method panics if called with incorrect parameters or at the
// wrong time in building chain.
func (b *TraceBuilder) Keys(keys []paramtools.Params) *TraceBuilder {
	if len(b.traceValues) == 0 {
		logAndPanic("Keys must be called after history loaded")
	}
	if len(b.traces) > 0 {
		logAndPanic("Keys must only once")
	}
	if len(keys) != len(b.traceValues) {
		logAndPanic("Expected one set of keys for each trace")
	}
	// We now have enough data to make all the traces.
	seenTraces := map[schema.SerializedJSON]bool{}
	for i, traceParams := range keys {
		traceParams.Add(b.commonKeys)
		grouping := make(map[string]string, len(keys))
		for _, gk := range b.groupingKeys {
			val, ok := traceParams[gk]
			if !ok {
				logAndPanic("Missing grouping key %q from %v", gk, traceParams)
			}
			grouping[gk] = val
		}
		groupingJSON, groupingID := sql.SerializeMap(grouping)
		traceJSON, traceID := sql.SerializeMap(traceParams)
		if seenTraces[traceJSON] {
			logAndPanic("Found identical trace %s", traceJSON)
		}
		seenTraces[traceJSON] = true
		for _, tv := range b.traceValues[i] {
			if tv != nil {
				tv.GroupingID = groupingID
				tv.TraceID = traceID
				tv.Shard = sql.ComputeTraceValueShard(traceID)
			}
		}
		b.groupings = append(b.groupings, schema.GroupingRow{
			GroupingID: groupingID,
			Keys:       groupingJSON,
		})
		b.traces = append(b.traces, schema.TraceRow{
			TraceID:              traceID,
			Corpus:               traceParams[types.CorpusField],
			GroupingID:           groupingID,
			Keys:                 traceJSON,
			MatchesAnyIgnoreRule: schema.NBNull,
		})
	}
	return b
}

// OptionsAll applies the given options for all data points provided in history.
func (b *TraceBuilder) OptionsAll(opts paramtools.Params) *TraceBuilder {
	xopts := make([]paramtools.Params, len(b.traceValues))
	for i := range xopts {
		xopts[i] = opts
	}
	return b.OptionsPerTrace(xopts)
}

// OptionsPerTrace applies the given optional params to the traces created in History. The number
// of options is expected to match the number of traces. It panics if called more than once or
// at the wrong time.
func (b *TraceBuilder) OptionsPerTrace(xopts []paramtools.Params) *TraceBuilder {
	if len(b.traceValues) == 0 {
		logAndPanic("Options* must be called after history loaded")
	}
	if len(b.options) > 0 {
		logAndPanic("Must call Options* only once")
	}
	if len(xopts) != len(b.traceValues) {
		logAndPanic("Must have one options per trace")
	}
	for i, opts := range xopts {
		optJSON, optionsID := sql.SerializeMap(opts)
		b.options = append(b.options, schema.OptionsRow{
			OptionsID: optionsID,
			Keys:      optJSON,
		})
		// apply it to every trace value in the ith trace
		for _, tv := range b.traceValues[i] {
			if tv == nil {
				continue
			}
			tv.OptionsID = optionsID
		}
	}
	return b
}

// IngestedFrom applies the given list of files and ingested times to the provided data.
// The number of filenames and ingestedDates is expected to match the number of commits; if no
// data is at that commit, it is ok to have both entries be empty string. It panics if any inputs
// are invalid.
func (b *TraceBuilder) IngestedFrom(filenames, ingestedDates []string) *TraceBuilder {
	if len(b.traceValues) == 0 {
		logAndPanic("IngestedFrom must be called after history")
	}
	if len(b.sourceFiles) > 0 {
		logAndPanic("Must call IngestedFrom only once")
	}
	if len(filenames) != len(b.commits) {
		logAndPanic("Expected %d files", len(b.commits))
	}
	if len(ingestedDates) != len(b.commits) {
		logAndPanic("Expected %d dates", len(b.commits))
	}

	for i := range filenames {
		name, ingestedDate := filenames[i], ingestedDates[i]
		if name == "" && ingestedDate == "" {
			continue // not used by any traces
		}
		if name == "" || ingestedDate == "" {
			logAndPanic("both name and date should be empty, if one is")
		}
		h := md5.Sum([]byte(name))
		sourceID := h[:]

		d, err := time.Parse(time.RFC3339, ingestedDate)
		if err != nil {
			logAndPanic("Invalid date format %q: %s", ingestedDate, err)
		}
		b.sourceFiles = append(b.sourceFiles, schema.SourceFileRow{
			SourceFileID: sourceID,
			SourceFile:   name,
			LastIngested: d,
		})
		// apply it to every ith tracevalue.
		for _, traceRows := range b.traceValues {
			if traceRows[i] != nil {
				traceRows[i].SourceFileID = sourceID
			}
		}
	}
	return b
}

func logAndPanic(msg string, args ...interface{}) {
	panic(fmt.Sprintf(msg, args...))
}
