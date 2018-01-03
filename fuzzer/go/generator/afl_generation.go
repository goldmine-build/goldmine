package generator

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"go.skia.org/infra/fuzzer/go/common"
	"go.skia.org/infra/fuzzer/go/config"
	fstorage "go.skia.org/infra/fuzzer/go/storage"
	"go.skia.org/infra/go/buildskia"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/fileutil"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/sklog"
)

const LOGGER_NAME_AFL = "afl_fuzz_logs"

type Generator struct {
	Category         string
	fuzzProcessCount metrics2.Counter
	fuzzProcesses    []exec.Process
}

// New creates a new generator for a fuzzer of a given category.
func New(category string) *Generator {
	return &Generator{
		Category:      category,
		fuzzProcesses: nil,
	}
}

// writeLog implements the io.Writer interface and writes to the given log function.
type writeLog struct {
	Severity string
	Source   string
	Category string
}

func (wl writeLog) Write(b []byte) (n int, err error) {
	sklog.CustomLog(LOGGER_NAME_AFL, &sklog.LogPayload{
		Time:     time.Now(),
		Severity: wl.Severity,
		Payload:  string(b),
		ExtraLabels: map[string]string{
			"source":   wl.Source,
			"category": wl.Category,
		},
	})
	return len(b), nil
}

// Start starts up 1 goroutine running a "master" afl-fuzz and n-1 "slave" afl-fuzz processes, where
// n is specified by config.Generator.NumFuzzProcesses. Output goes to
// config.Generator.AflOutputPath/[category].
func (g *Generator) Start(ctx context.Context) error {
	if config.Generator.SkipGeneration {
		sklog.Info("Skipping generation because flag was set.")
		return nil
	}
	executable, err := g.setup(ctx)
	if err != nil {
		return fmt.Errorf("Failed %s generator setup: %s", g.Category, err)
	}

	masterCmd := &exec.Command{
		Name:    "./afl-fuzz",
		Args:    common.GenerationArgsFor(g.Category, executable, "fuzzer0", true),
		Dir:     config.Generator.AflRoot,
		Stdout:  &writeLog{Severity: sklog.DEBUG, Category: g.Category, Source: "master"},
		Stderr:  &writeLog{Severity: sklog.ERROR, Category: g.Category, Source: "master"},
		Env:     []string{"AFL_SKIP_CPUFREQ=true"}, // Avoids a warning afl-fuzz spits out about dynamic scaling of cpu frequency
		Verbose: exec.Debug,
	}
	if config.Generator.WatchAFL {
		masterCmd.Stdout = os.Stdout
	}

	g.fuzzProcesses = append(g.fuzzProcesses, g.run(masterCmd))

	fuzzCount := config.Generator.NumBinaryFuzzProcesses
	if strings.HasPrefix(g.Category, "api_") {
		fuzzCount = config.Generator.NumAPIFuzzProcesses
	}
	if fuzzCount <= 0 {
		// TODO(kjlubick): Make this actually an intelligent number based on the number of cores.
		fuzzCount = 4
	}

	g.fuzzProcessCount = metrics2.GetCounter("afl_fuzz_process_count", map[string]string{"fuzz_category": g.Category, "architecture": config.Generator.Architecture})
	g.fuzzProcessCount.Inc(int64(fuzzCount))
	for i := 1; i < fuzzCount; i++ {
		fuzzerName := fmt.Sprintf("fuzzer%d", i)
		slaveCmd := &exec.Command{
			Name:    "./afl-fuzz",
			Args:    common.GenerationArgsFor(g.Category, executable, fuzzerName, false),
			Dir:     config.Generator.AflRoot,
			Stdout:  &writeLog{Severity: sklog.DEBUG, Category: g.Category, Source: fuzzerName},
			Stderr:  &writeLog{Severity: sklog.ERROR, Category: g.Category, Source: fuzzerName},
			Env:     []string{"AFL_SKIP_CPUFREQ=true"}, // Avoids a warning afl-fuzz spits out about dynamic scaling of cpu frequency
			Verbose: exec.Debug,
		}
		g.fuzzProcesses = append(g.fuzzProcesses, g.run(slaveCmd))
	}
	return nil
}

// setup clears out previous fuzzing sessions and builds the executable we need to run afl-fuzz.
// The binary is then copied to the working directory as "fuzz_afl_Release".
func (g *Generator) setup(ctx context.Context) (string, error) {
	if err := g.Clear(); err != nil {
		return "", err
	}
	// get a version of Skia built with afl-fuzz's instrumentation
	if srcExe, err := common.BuildFuzzingHarness(ctx, buildskia.RELEASE_BUILD, true); err != nil {
		return "", fmt.Errorf("Failed to build fuzz executable using afl-fuzz %s", err)
	} else {
		// copy to working directory
		destExe := filepath.Join(config.Generator.WorkingPath, g.Category, common.TEST_HARNESS_NAME+"_afl_Release")
		if err := fileutil.CopyExecutable(srcExe, destExe); err != nil {
			return "", err
		}
		return destExe, nil
	}
}

// Clear removes the previous fuzzing sessions data and any previously used binaries.
func (g *Generator) Clear() error {
	workingPath := filepath.Join(config.Generator.WorkingPath, g.Category)
	if err := os.RemoveAll(workingPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove previous binaries from %s: %s", workingPath, err)
	}
	if err := os.MkdirAll(workingPath, 0755); err != nil {
		return fmt.Errorf("Failed to create working directory %s: %s", workingPath, err)
	}

	// remove previous fuzz results
	resultsPath := filepath.Join(config.Generator.AflOutputPath, g.Category)
	if err := os.RemoveAll(resultsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove previous fuzz results from %s: %s", resultsPath, err)
	}
	if err := os.MkdirAll(resultsPath, 0755); err != nil {
		return fmt.Errorf("Failed to create fuzz results directory %s: %s", resultsPath, err)
	}
	return nil
}

// run runs the command and logs any failures.  It returns the Process that can be used to
// manually kill the command.
func (g *Generator) run(command *exec.Command) exec.Process {
	p, status, err := exec.RunIndefinitely(command)
	if err != nil {
		sklog.Errorf("Failed afl fuzzer command %#v: %s", command, err)
		return nil
	}
	go func() {
		err := <-status
		g.fuzzProcessCount.Dec(int64(1))
		sklog.Warningf(`[%s] afl fuzzer with args %q ended with error "%v".  There are %d fuzzers remaining`, g.Category, command.Args, err, g.fuzzProcessCount.Get())
	}()
	return p
}

// Stop terminates all afl-fuzz processes that were spawned, logging any errors. It also
// sets some key metrics to 0, so the graphs at mon.skia.org reflect the stoppage.
func (g *Generator) Stop() {
	sklog.Infof("Trying to stop %d fuzz processes", len(g.fuzzProcesses))
	for _, p := range g.fuzzProcesses {
		if p != nil {
			if err := p.Kill(); err != nil {
				sklog.Warningf("[%s] Error while trying to kill afl process: %s", g.Category, err)
			} else {
				sklog.Infof("[%s] Quietly shutdown fuzz process.", g.Category)
			}
		}
	}
	g.fuzzProcesses = nil

	// Get rid of stats file to avoid old stats from being picked up by the aggregator
	statsFile := filepath.Join(config.Generator.AflOutputPath, g.Category, "fuzzer0", "fuzzer_stats")
	if err := os.Remove(statsFile); err != nil {
		sklog.Warningf("Could not clear out old fuzzer_stats file %s: %s", statsFile, err)
	}

	metrics2.GetInt64Metric("fuzzer_stats_execs_per_sec", map[string]string{"fuzz_category": g.Category, "architecture": config.Generator.Architecture}).Update(0)
	metrics2.GetInt64Metric("fuzzer_stats_paths_total", map[string]string{"fuzz_category": g.Category, "architecture": config.Generator.Architecture}).Update(0)
	metrics2.GetInt64Metric("fuzzer_stats_cycles_done", map[string]string{"fuzz_category": g.Category, "architecture": config.Generator.Architecture}).Update(0)
}

// DownloadSeedFiles downloads the seed files stored in Google Storage to be used by afl-fuzz.  It
// places them in config.Generator.FuzzSamples/[category] after cleaning the folder out. It returns
// an error on failure.
func (g *Generator) DownloadSeedFiles(storageClient fstorage.FuzzerGCSClient) error {
	seedPath := filepath.Join(config.Generator.FuzzSamples, g.Category)
	if err := os.RemoveAll(seedPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Could not clean binary seed path %s: %s", seedPath, err)
	}
	if err := os.MkdirAll(seedPath, 0755); err != nil {
		return fmt.Errorf("Could not create binary seed path %s: %s", seedPath, err)
	}

	// API fuzzers can all share the same seeds, as they are just random numbers.
	// EXCEPTION: Canvas fuzzers are pretty slow, so they have their own set of seeds that gets
	// the fuzzer going much faster. It saves about 3 hours of startup work every time the fuzzers
	// are restarted.
	cat := g.Category
	if strings.HasPrefix(cat, "api_") {
		cat = "api"
	}
	if strings.HasSuffix(cat, "_canvas") {
		cat = "canvas"
	}
	gsFolder := fmt.Sprintf("samples/%s/", cat)

	err := storageClient.AllFilesInDirectory(context.Background(), gsFolder, func(item *storage.ObjectAttrs) {
		name := item.Name
		// skip the parent folder
		if name == gsFolder {
			return
		}
		content, err := storageClient.GetFileContents(context.Background(), name)
		if err != nil {
			sklog.Errorf("[%s] Problem downloading %s from Google Storage, continuing anyway", g.Category, item.Name)
			return
		}
		fileName := filepath.Join(seedPath, strings.SplitAfter(name, gsFolder)[1])
		if err = ioutil.WriteFile(fileName, content, 0644); err != nil && !os.IsExist(err) {
			sklog.Errorf("[%s] Problem creating binary seed file %s, continuing anyway", g.Category, fileName)
		}
	})
	return err
}
