package common

import (
	"fmt"
	"path/filepath"
	"time"

	"go.skia.org/infra/fuzzer/go/config"
	"go.skia.org/infra/go/sklog"
)

type AnalysisArgs []string
type GenerationArgs []string

// AnalysisArgsFor creates an appropriate analysis command for the category of fuzz specified given
// the passed in variables. It is expected that these arguments will be executed with GNU timeout
// GNU timeout is used instead of the option on exec.Command because experimentation with the latter
// showed evidence of that way leaking processes, which lead to OOM errors. GNU catchsegv generates
// human readable dumps of crashes, which can then be scanned for stacktrace information.
func AnalysisArgsFor(category string, pathToExecutable, pathToFile string) AnalysisArgs {
	timeoutInSeconds := fmt.Sprintf("%ds", config.Aggregator.AnalysisTimeout/time.Second)
	f, found := fuzzers[category]
	if !found {
		sklog.Errorf("Unknown fuzz category %q", category)
		return nil
	}
	cmd := append([]string{timeoutInSeconds, "catchsegv", pathToExecutable}, f.ArgsAfterExecutable...)
	return append(cmd, pathToFile)
}

// GenerationArgsFor creates the appropriate arguments to run afl-fuzz on a fuzz of the given
// category. We set the maximum memory to 5GB to avoid all but the most extreme cases of memory
// problems. The timeout is set at whatever afl-fuzz thinks is best.  This is typically < 100ms,
// and is based on the timing of the sample data.
func GenerationArgsFor(category, pathToExecutable, fuzzerName string, isMaster bool) GenerationArgs {
	f, found := fuzzers[category]
	if !found {
		sklog.Errorf("Unknown fuzz category %q", category)
		return nil
	}
	masterFlag := "-M"
	if !isMaster {
		masterFlag = "-S"
	}
	seedPath := filepath.Join(config.Generator.FuzzSamples, category)
	outputPath := filepath.Join(config.Generator.AflOutputPath, category)

	cmd := append([]string{"-i", seedPath, "-o", outputPath}, f.GenerationArgs...)

	cmd2 := append([]string{masterFlag, fuzzerName, "--", pathToExecutable}, f.ArgsAfterExecutable...)

	return append(append(cmd, cmd2...), "@@")
}
