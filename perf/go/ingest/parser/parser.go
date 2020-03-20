// Package parser has funcs for parsing ingestion files.
package parser

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"strings"

	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/query"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/perf/go/config"
	"go.skia.org/infra/perf/go/file"
	"go.skia.org/infra/perf/go/ingest/format"
)

var (
	// ErrFileShouldBeSkipped is returned if a file should be skipped.
	ErrFileShouldBeSkipped = errors.New("File should be skipped.")
)

// Parser parses file.Files contents into a form suitable for writing to trace.Store.
type Parser struct {
	instanceConfig   *config.InstanceConfig
	parseCounter     metrics2.Counter
	parseFailCounter metrics2.Counter
	branchNames      map[string]bool
}

// New creates a new instance of Parser.
func New(instanceConfig *config.InstanceConfig) *Parser {
	ret := &Parser{
		instanceConfig:   instanceConfig,
		parseCounter:     metrics2.GetCounter("perf_ingest_parser_parse", nil),
		parseFailCounter: metrics2.GetCounter("perf_ingest_parser_parse_failed", nil),
		branchNames:      map[string]bool{},
	}
	for _, branchName := range instanceConfig.IngestionConfig.Branches {
		ret.branchNames[branchName] = true
	}
	return ret
}

// getParamsAndValuesFromLegacyFormat returns two parallel slices, each slice
// contains the params and then the float for a single value of a trace.
func getParamsAndValuesFromLegacyFormat(b *format.BenchData) ([]paramtools.Params, []float64) {
	params := []paramtools.Params{}
	values := []float64{}
	for testName, allConfigs := range b.Results {
		for configName, result := range allConfigs {
			key := paramtools.Params(b.Key).Copy()
			key["test"] = testName
			key["config"] = configName
			key.Add(paramtools.Params(b.Options))
			// If there is an options map inside the result add it to the params.
			if resultOptions, ok := result["options"]; ok {
				if opts, ok := resultOptions.(map[string]interface{}); ok {
					for k, vi := range opts {
						// Ignore the very long and not useful GL_ values, we can retrieve
						// them later via ptracestore.Details.
						if strings.HasPrefix(k, "GL_") {
							continue
						}
						if s, ok := vi.(string); ok {
							key[k] = s
						}
					}
				}
			}
			for k, vi := range result {
				if k == "options" || k == "samples" {
					continue
				}
				key["sub_result"] = k
				floatVal, ok := vi.(float64)
				if !ok {
					sklog.Errorf("Found a non-float64 in %v", result)
					continue
				}
				key = query.ForceValid(key)
				params = append(params, key.Copy())
				values = append(values, floatVal)
			}
		}
	}
	return params, values
}

// getParamsAndValuesFromVersion1Format returns two parallel slices, each slice contains
// the params and then the float for a single value of a trace.
func getParamsAndValuesFromVersion1Format(f format.Format) ([]paramtools.Params, []float64) {
	paramSlice := []paramtools.Params{}
	measurementSlice := []float64{}
	for _, result := range f.Results {
		p := paramtools.Params(f.Key).Copy()
		p.Add(result.Key)
		if len(result.Measurements) == 0 {
			paramSlice = append(paramSlice, p)
			measurementSlice = append(measurementSlice, result.Measurement)
		} else {
			for key, measurements := range result.Measurements {
				for _, measurement := range measurements {
					singleParam := p.Copy()
					singleParam[key] = measurement.Value
					paramSlice = append(paramSlice, singleParam)
					measurementSlice = append(measurementSlice, measurement.Measurement)
				}
			}

		}
	}
	return paramSlice, measurementSlice
}

// checkBranchName returns the branch name and true if the file should continue
// to be processed. Note that if the 'params' don't contain a key named 'branch'
// then the file should be processed, in which case the returned branch name is
// "".
func (p *Parser) checkBranchName(params map[string]string) (string, bool) {
	branch, ok := params["branch"]
	if ok {
		return branch, p.branchNames[branch]
	}
	return "", true
}

func (p *Parser) extractFromLegacyFile(r io.Reader, filename string) ([]paramtools.Params, []float64, string, map[string]string, error) {
	benchData, err := format.ParseLegacyFormat(r)
	if err != nil {
		return nil, nil, "", nil, err
	}
	params, values := getParamsAndValuesFromLegacyFormat(benchData)
	return params, values, benchData.Hash, benchData.Key, nil
}

func (p *Parser) extractFromVersion1File(r io.Reader, filename string) ([]paramtools.Params, []float64, string, map[string]string, error) {
	f, err := format.Parse(r)
	if err != nil {
		return nil, nil, "", nil, err
	}
	params, values := getParamsAndValuesFromVersion1Format(f)
	return params, values, f.GitHash, f.Key, nil
}

// Parse the given file.File contents.
//
// Returns two parallel slices, each slice contains the params and then the
// float for a single value of a trace.
//
// The returned error will be ErrFileShouldBeSkipped if the file should not be
// processed any further.
//
// The File.Contents will be closed when this func returns.
func (p *Parser) Parse(file file.File) ([]paramtools.Params, []float64, string, error) {
	defer util.Close(file.Contents)
	p.parseCounter.Inc(1)

	// Read the whole content into bytes.Reader since we may take more than one
	// pass at the data.
	b, err := ioutil.ReadAll(file.Contents)
	if err != nil {
		return nil, nil, "", skerr.Wrap(err)
	}
	r := bytes.NewReader(b)

	// Expect the file to be in format.FileFormat.
	params, values, hash, commonKeys, err := p.extractFromVersion1File(r, file.Name)
	if err != nil {
		// Fallback to the legacy format.
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return nil, nil, "", skerr.Wrap(err)
		}
		params, values, hash, commonKeys, err = p.extractFromLegacyFile(r, file.Name)
	}
	if err != nil && err != ErrFileShouldBeSkipped {
		p.parseFailCounter.Inc(1)
	}
	if err != nil {
		return nil, nil, "", err
	}
	branch, ok := p.checkBranchName(commonKeys)
	if !ok {
		return nil, nil, "", ErrFileShouldBeSkipped
	}
	if len(params) == 0 {
		metrics2.GetCounter("perf_ingest_parser_no_data_in_file", map[string]string{"branch": branch}).Inc(1)
		sklog.Infof("No data in: %q", file.Name)
		return nil, nil, "", ErrFileShouldBeSkipped
	}
	return params, values, hash, nil
}
