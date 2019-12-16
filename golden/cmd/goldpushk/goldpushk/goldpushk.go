// Package goldpushk contains the Goldpushk struct, which coordinates all the operations performed
// by goldpushk.
//
// Also included in this package is function ProductionDeployableUnits(), which returns a set with
// all the services goldpushk is able to manage.
//
// Function ProductionDeployableUnits is the source of truth of goldpushk, and should be updated to
// reflect any relevant changes in configuration.
//
// For testing, function TestingDeployableUnits should be used instead, which only contains
// information about dummy services that can be deployed the public or corp clusters without
// disrupting any production services.
package goldpushk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/git"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
)

const (
	// Paths below are relative to $SKIA_INFRA_ROOT.
	k8sConfigTemplatesDir = "golden/k8s-config-templates"
	k8sInstancesDir       = "golden/k8s-instances"

	// kubectl timestamp format as of September 30, 2019.
	//
	// $ kubectl version --short
	// Client Version: v1.16.0
	// Server Version: v1.14.6-gke.1
	kubectlTimestampLayout = "2006-01-02T15:04:05Z"

	// Time to wait between the push and monitoring steps, to give Kubernetes a chance to update the
	// status of the affected pods.
	delayBetweenPushAndMonitoring = 10 * time.Second
)

// cluster represents a Kubernetes cluster on which to deploy DeployableUnits, and contains all the
// information necessary to switch between clusters with the "gcloud" command.
type cluster struct {
	name      string // e.g. "skia-corp".
	projectID string // e.g. "google.com:skia-corp".
}

var (
	clusterSkiaPublic = cluster{name: "skia-public", projectID: "skia-public"}
	clusterSkiaCorp   = cluster{name: "skia-corp", projectID: "google.com:skia-corp"}
)

// Goldpushk contains information about the deployment steps to be carried out.
type Goldpushk struct {
	// Input parameters (provided via flags or environment variables).
	deployableUnits            []DeployableUnit
	canariedDeployableUnits    []DeployableUnit
	rootPath                   string // Path to the buildbot checkout.
	dryRun                     bool
	noCommit                   bool
	minUptimeSeconds           int
	uptimePollFrequencySeconds int

	// Other constructor parameters.
	skiaPublicConfigRepoUrl string
	skiaCorpConfigRepoUrl   string
	k8sConfigRepoUrl        string

	// Checked out Git repositories.
	skiaPublicConfigCheckout *git.TempCheckout
	skiaCorpConfigCheckout   *git.TempCheckout
	k8sConfigCheckout        *git.TempCheckout

	// The Kubernetes cluster that the kubectl command is currently configured to use.
	currentCluster cluster

	// Miscellaneous.
	unitTest bool // Disables confirmation prompt from unit tests.
}

// New is the Goldpushk constructor.
func New(deployableUnits []DeployableUnit, canariedDeployableUnits []DeployableUnit, skiaInfraRootPath string, dryRun, noCommit bool, minUptimeSeconds, uptimePollFrequencySeconds int, skiaPublicConfigRepoUrl, skiaCorpConfigRepoUrl, k8sConfigRepoUrl string) *Goldpushk {
	return &Goldpushk{
		deployableUnits:            deployableUnits,
		canariedDeployableUnits:    canariedDeployableUnits,
		rootPath:                   skiaInfraRootPath,
		dryRun:                     dryRun,
		noCommit:                   noCommit,
		minUptimeSeconds:           minUptimeSeconds,
		uptimePollFrequencySeconds: uptimePollFrequencySeconds,
		skiaPublicConfigRepoUrl:    skiaPublicConfigRepoUrl,
		skiaCorpConfigRepoUrl:      skiaCorpConfigRepoUrl,
		k8sConfigRepoUrl:           k8sConfigRepoUrl,
	}
}

// Run carries out the deployment steps.
func (g *Goldpushk) Run(ctx context.Context) error {
	// Print out list of targeted deployable units, and ask for confirmation.
	if ok, err := g.printOutInputsAndAskConfirmation(); err != nil {
		return skerr.Wrap(err)
	} else if !ok {
		return nil
	}

	// Check out Git repositories.
	if err := g.checkOutGitRepositories(ctx); err != nil {
		return skerr.Wrap(err)
	}
	defer g.skiaPublicConfigCheckout.Delete()
	defer g.skiaCorpConfigCheckout.Delete()
	defer g.k8sConfigCheckout.Delete()

	// Regenerate config files.
	if err := g.regenerateConfigFiles(ctx); err != nil {
		return skerr.Wrap(err)
	}

	// Commit config files, giving the user the option to abort.
	if ok, err := g.commitConfigFiles(ctx); err != nil {
		return skerr.Wrap(err)
	} else if !ok {
		return nil
	}

	// Deploy canaries.
	if err := g.pushCanaries(ctx); err != nil {
		return skerr.Wrap(err)
	}

	// Monitor canaries.
	if err := g.monitorCanaries(ctx); err != nil {
		return skerr.Wrap(err)
	}

	// Deploy remaining DeployableUnits.
	if err := g.pushServices(ctx); err != nil {
		return skerr.Wrap(err)
	}

	// Monitor remaining DeployableUnits.
	if err := g.monitorServices(ctx); err != nil {
		return skerr.Wrap(err)
	}

	// Give the user a chance to examine the generated files before exiting and cleaning up the Git
	// repositories.
	if g.dryRun {
		fmt.Println("\nDry-run finished. Any generated files can be found in the following Git repository checkouts:")
		fmt.Printf("  %s\n", g.skiaPublicConfigCheckout.GitDir)
		fmt.Printf("  %s\n", g.skiaCorpConfigCheckout.GitDir)
		fmt.Printf("  %s\n", g.k8sConfigCheckout.GitDir)
		fmt.Println("Press enter to delete the checkouts above and exit.")
		if _, err := fmt.Scanln(); err != nil {
			return skerr.Wrap(err)
		}
	}

	return nil
}

// printOutInputsAndAskConfirmation prints out a summary of the actions to be
// taken, then asks the user for confirmation.
func (g *Goldpushk) printOutInputsAndAskConfirmation() (bool, error) {
	// Skip if running from an unit test.
	if g.unitTest {
		return true, nil
	}

	// Print out a summary of the services to deploy.
	if len(g.canariedDeployableUnits) != 0 {
		fmt.Println("The following services will be canaried:")
		for _, d := range g.canariedDeployableUnits {
			fmt.Printf("  %s\n", d.CanonicalName())
		}
		fmt.Println()
	}
	fmt.Println("The following services will be deployed:")
	for _, d := range g.deployableUnits {
		fmt.Printf("  %s\n", d.CanonicalName())
	}

	// Ask for confirmation, ending execution by default.
	ok, err := prompt("\nProceed?")
	if err != nil {
		return false, skerr.Wrap(err)
	}
	if !ok {
		fmt.Println("Aborting.")
		return false, nil
	}

	return true, nil
}

// prompt prints out a question to stdout and scans a y/n answer from stdin.
func prompt(question string) (bool, error) {
	fmt.Printf("%s (y/N): ", question)
	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		return false, skerr.Wrapf(err, "unable to read from standard input")
	}
	if input != "y" {
		return false, nil
	}
	return true, nil
}

// checkOutGitRepositories checks out the skia-public-config and
// skia-corp-config Git repositories.
func (g *Goldpushk) checkOutGitRepositories(ctx context.Context) error {
	fmt.Println()
	var err error
	if g.skiaPublicConfigCheckout, err = checkOutSingleGitRepository(ctx, g.skiaPublicConfigRepoUrl); err != nil {
		return skerr.Wrap(err)
	}
	if g.skiaCorpConfigCheckout, err = checkOutSingleGitRepository(ctx, g.skiaCorpConfigRepoUrl); err != nil {
		return skerr.Wrap(err)
	}
	if g.k8sConfigCheckout, err = checkOutSingleGitRepository(ctx, g.k8sConfigRepoUrl); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// checkOutSingleGitRepository checks out the Git repository at the given URL.
func checkOutSingleGitRepository(ctx context.Context, url string) (*git.TempCheckout, error) {
	checkout, err := git.NewTempCheckout(ctx, url)
	if err != nil {
		return nil, skerr.Wrapf(err, "failed to check out %s", url)
	}
	fmt.Printf("Cloned Git repository %s at %s.\n", url, string(checkout.GitDir))
	return checkout, nil
}

// regenerateConfigFiles regenerates the .yaml and .json5 files for each
// instance/service pair that will be deployed. Any generated files will be
// checked into the corresponding Git repository with configuration files.
func (g *Goldpushk) regenerateConfigFiles(ctx context.Context) error {
	// Iterate over all units to deploy (including canaries).
	return g.forAllDeployableUnits(func(unit DeployableUnit) error {
		// Path to the template file inside $SKIA_INFRA_ROOT.
		tPath := unit.getDeploymentFileTemplatePath(g.rootPath)

		// Path to the deployment file (.yaml) we will regenerate inside the
		// corresponding skia-{public,corp}-config Git repository.
		oPath := g.getDeploymentFilePath(unit)

		// Regenerate .yaml file.
		if err := g.expandTemplate(ctx, unit.Instance, tPath, oPath); err != nil {
			return skerr.Wrapf(err, "error while regenerating %s", oPath)
		}

		// If the DeployableUnit has a ConfigMap template (as opposed to a ConfigMap
		// file that already exists in $SKIA_INFRA_ROOT)), the template must be
		// expanded and saved to the corresponding skia-{public,corp}-config Git
		// repository.
		if unit.configMapTemplate != "" {
			// Path to the template file inside $SKIA_INFRA_ROOT.
			tPath = unit.getConfigMapFileTemplatePath(g.rootPath)

			// Path to the ConfigMap file (.json5) to be regenerated inside the
			// corresponding skia-{public,corp}-config repository.
			oPath, ok := g.getConfigMapFilePath(unit)
			if !ok {
				return fmt.Errorf("goldpushk.getConfigMapFilePath() failed for %s; this is probably a bug", unit.CanonicalName())
			}

			// Regenerate .json5 file.
			if err := g.expandTemplate(ctx, unit.Instance, tPath, oPath); err != nil {
				return skerr.Wrapf(err, "error while regenerating %s", oPath)
			}
		}

		return nil
	})
}

// getDeploymentFilePath returns the path to the deployment file (.yaml) for the
// given DeployableUnit inside the corresponding skia-{public,corp}-config Git
// repository.
func (g *Goldpushk) getDeploymentFilePath(unit DeployableUnit) string {
	return filepath.Join(g.getGitRepoSubdirPath(unit), unit.CanonicalName()+".yaml")
}

// getConfigMapFilePath returns the path to the ConfigFile (.json5) for the
// given DeployableUnit.
//
// If the DeployableUnit has a ConfigMap file (e.g. gold-skiapublic-skiacorrectness)
// this will be a path inside $SKIA_INFRA_ROOT.
//
// If the DeployableUnit has a ConfigMap template (e.g. gold-skia-ingestion-bt)
// this will be a path inside the corresponding skia-{public,corp}-config Git
// repository pointing to the expanded ConfigMap template.
//
// If neither is true, it will return ("", false).
func (g *Goldpushk) getConfigMapFilePath(unit DeployableUnit) (string, bool) {
	if unit.configMapFile != "" {
		return filepath.Join(g.rootPath, unit.configMapFile), true
	} else if unit.configMapName != "" && unit.configMapTemplate != "" {
		return filepath.Join(g.getGitRepoSubdirPath(unit), unit.configMapName+".json5"), true
	} else {
		return "", false
	}
}

// getGitRepoSubdirPath returns the path to the subdirectory inside the k8s-config
// repository checkout in which the config files for the given DeployableUnit
// should be checked in  (e.g. /path/to/k8s-config/skia-public-config).
func (g *Goldpushk) getGitRepoSubdirPath(unit DeployableUnit) string {
	subdir := clusterSkiaPublic.name
	if unit.internal {
		subdir = clusterSkiaCorp.name
	}
	return filepath.Join(string(g.k8sConfigCheckout.GitDir), subdir)
}

// expandTemplate executes the kube-conf-gen command with the given arguments in
// a fashion similar to gen-k8s-config.sh.
func (g *Goldpushk) expandTemplate(ctx context.Context, instance Instance, templatePath, outputPath string) error {
	goldCommonJson5 := filepath.Join(g.rootPath, k8sConfigTemplatesDir, "gold-common.json5")
	instanceStr := string(instance)
	instanceJson5 := filepath.Join(g.rootPath, k8sInstancesDir, instanceStr+"-instance.json5")

	cmd := &exec.Command{
		Name: "kube-conf-gen",
		// Notes on the kube-conf-gen arguments used:
		//   - Flag "-extra INSTANCE_ID:<instanceStr>" binds template variable
		//     INSTANCE_ID to instanceStr.
		//   - Flag "-strict" will make kube-conf-gen fail in the presence of
		//     unsupported types, missing data, etc.
		//   - Flag "-parse_conf=false" prevents the values read from the JSON5
		//     config files provided with -c <json5-file> from being converted to
		//     strings.
		Args:        []string{"-c", goldCommonJson5, "-c", instanceJson5, "-extra", "INSTANCE_ID:" + instanceStr, "-t", templatePath, "-parse_conf=false", "-strict", "-o", outputPath},
		InheritPath: true,
		LogStderr:   true,
		LogStdout:   true,
	}

	if err := exec.Run(ctx, cmd); err != nil {
		return skerr.Wrapf(err, "failed to run %s", cmdToDebugStr(cmd))
	}
	sklog.Infof("Generated %s", outputPath)
	return nil
}

// commitConfigFiles prints out a summary of the changes to be committed to
// skia-{public,corp}-config, asks for confirmation and pushes those changes.
func (g *Goldpushk) commitConfigFiles(ctx context.Context) (bool, error) {
	// Print out summary of changes (git status -s).
	err := g.forAllGitRepos(func(repo *git.TempCheckout, name string) error {
		return printOutGitStatus(ctx, repo, name)
	})
	if err != nil {
		return false, skerr.Wrap(err)
	}

	// Skip if --no-commit or --dryrun.
	if g.dryRun || g.noCommit {
		reason := "dry run"
		if g.noCommit {
			reason = "no commit"
		}
		fmt.Printf("\nSkipping commit step (%s)\n", reason)
		return true, nil
	}

	// Ask for confirmation.
	ok, err := prompt("\nCommit and push the above changes? Answering no will abort execution.")
	if err != nil {
		return false, skerr.Wrap(err)
	}
	if !ok {
		return false, nil
	}

	// Add, commit and push changes.
	fmt.Println()
	err = g.forAllGitRepos(func(repo *git.TempCheckout, name string) error {
		// Skip if the current repository has no changes.
		stdout, err := repo.Git(ctx, "status", "-s")
		if err != nil {
			return skerr.Wrap(err)
		}
		if len(stdout) == 0 {
			return nil
		}

		fmt.Printf("Pushing changes to %s.\n", name)
		if _, err := repo.Git(ctx, "add", "."); err != nil {
			return skerr.Wrap(err)
		}
		if _, err := repo.Git(ctx, "commit", "-m", "Push"); err != nil {
			return skerr.Wrap(err)
		}
		if _, err := repo.Git(ctx, "push", "origin", "master"); err != nil {
			return skerr.Wrap(err)
		}
		return nil
	})
	if err != nil {
		return false, skerr.Wrap(err)
	}

	return true, nil
}

// printOutGitStatus runs "git status -s" on the given checkout and prints its output to stdout.
func printOutGitStatus(ctx context.Context, checkout *git.TempCheckout, repoName string) error {
	stdout, err := checkout.Git(ctx, "status", "-s")
	if err != nil {
		return skerr.Wrap(err)
	}
	if len(stdout) == 0 {
		fmt.Printf("\nNo changes to be pushed to %s.\n", repoName)
	} else {
		fmt.Printf("\nChanges to be pushed to %s:\n", repoName)
		fmt.Print(stdout)
	}
	return nil
}

// pushCanaries deploys the canaried DeployableUnits.
func (g *Goldpushk) pushCanaries(ctx context.Context) error {
	if len(g.canariedDeployableUnits) == 0 {
		return nil
	}

	fmt.Println("\nPushing canaried services.")
	if err := g.pushDeployableUnits(ctx, g.canariedDeployableUnits); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// monitorCanaries monitors the canaried DeployableUnits after they have been pushed to production.
func (g *Goldpushk) monitorCanaries(ctx context.Context) error {
	if len(g.canariedDeployableUnits) == 0 {
		return nil
	}
	if err := g.monitor(ctx, g.canariedDeployableUnits, g.getUptimes, time.Sleep); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// pushServices deploys the non-canaried DeployableUnits.
func (g *Goldpushk) pushServices(ctx context.Context) error {
	if len(g.canariedDeployableUnits) == 0 {
		fmt.Println("\nPushing services.")
	} else {
		fmt.Println("\nPushing remaining services.")
	}

	if err := g.pushDeployableUnits(ctx, g.deployableUnits); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// monitorServices monitors the non-canaried DeployableUnits after they have been pushed to
// production.
func (g *Goldpushk) monitorServices(ctx context.Context) error {
	if err := g.monitor(ctx, g.deployableUnits, g.getUptimes, time.Sleep); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// pushDeployableUnits takes a slice of DeployableUnits and pushes them to their corresponding
// clusters.
func (g *Goldpushk) pushDeployableUnits(ctx context.Context, units []DeployableUnit) error {
	if g.dryRun {
		fmt.Println("\nSkipping push step (dry run).")
		return nil
	}

	for _, unit := range units {
		if err := g.pushSingleDeployableUnit(ctx, unit); err != nil {
			return skerr.Wrap(err)
		}
	}
	return nil
}

// pushSingleDeployableUnit pushes the given DeployableUnit to the corresponding cluster by running
// "kubectl apply -f path/to/config.yaml".
func (g *Goldpushk) pushSingleDeployableUnit(ctx context.Context, unit DeployableUnit) error {
	// Get the cluster corresponding to the given DeployableUnit.
	cluster := clusterSkiaPublic
	if unit.internal {
		cluster = clusterSkiaCorp
	}

	// Switch clusters.
	if err := g.switchClusters(ctx, cluster); err != nil {
		return skerr.Wrap(err)
	}

	// Push ConfigMap if the DeployableUnit requires one.
	if err := g.maybePushConfigMap(ctx, unit); err != nil {
		return skerr.Wrap(err)
	}

	// Push DeployableUnit.
	path := g.getDeploymentFilePath(unit)
	fmt.Printf("%s: applying %s.\n", unit.CanonicalName(), path)
	cmd := &exec.Command{
		Name:        "kubectl",
		Args:        []string{"apply", "-f", path},
		InheritPath: true,
		LogStderr:   true,
		LogStdout:   true,
	}
	if err := exec.Run(ctx, cmd); err != nil {
		return skerr.Wrapf(err, "failed to run %s", cmdToDebugStr(cmd))
	}

	return nil
}

// maybePushConfigMap pushes the ConfigMap required by the given DeployableUnit, if it needs one.
func (g *Goldpushk) maybePushConfigMap(ctx context.Context, unit DeployableUnit) error {
	// If the DeployableUnit requires a ConfigMap, delete and recreate it.
	if path, ok := g.getConfigMapFilePath(unit); ok {
		fmt.Printf("%s: creating ConfigMap named \"%s\" from file %s.\n", unit.CanonicalName(), unit.configMapName, path)

		// Delete existing ConfigMap.
		cmd := &exec.Command{
			Name:        "kubectl",
			Args:        []string{"delete", "configmap", unit.configMapName},
			InheritPath: true,
			LogStderr:   true,
			LogStdout:   true,
		}
		if err := exec.Run(ctx, cmd); err != nil {
			// TODO(lovisolo): Figure out a less brittle way to detect exit status 1.
			if strings.HasPrefix(err.Error(), "Command exited with exit status 1") {
				sklog.Infof("Did not delete ConfigMap %s as it does not exist on the cluster.", unit.configMapName)
			} else {
				return skerr.Wrapf(err, "failed to run %s", cmdToDebugStr(cmd))
			}
		}

		// Create new ConfigMap.
		cmd = &exec.Command{
			Name:        "kubectl",
			Args:        []string{"create", "configmap", unit.configMapName, "--from-file", path},
			InheritPath: true,
			LogStderr:   true,
			LogStdout:   true,
		}
		if err := exec.Run(ctx, cmd); err != nil {
			return skerr.Wrapf(err, "failed to run %s", cmdToDebugStr(cmd))
		}
	}
	return nil
}

// switchClusters runs the "gcloud" command necessary to switch kubectl to the given cluster.
func (g *Goldpushk) switchClusters(ctx context.Context, cluster cluster) error {
	if g.currentCluster != cluster {
		sklog.Infof("Switching to cluster %s\n", cluster.name)
		cmd := &exec.Command{
			Name:        "gcloud",
			Args:        []string{"container", "clusters", "get-credentials", cluster.name, "--zone", "us-central1-a", "--project", cluster.projectID},
			InheritPath: true,
			LogStderr:   true,
			LogStdout:   true,
		}
		if err := exec.Run(ctx, cmd); err != nil {
			return skerr.Wrapf(err, "failed to run %s", cmdToDebugStr(cmd))
		}
		g.currentCluster = cluster
	}
	return nil
}

// uptimesFn has the same signature as method Goldpushk.getUptimes(). To facilitate testing, method
// Goldpushk.monitor() takes an uptimesFn instance as a parameter instead of calling
// Goldpushk.getUptimes() directly.
type uptimesFn func(context.Context, []DeployableUnit, time.Time) (map[DeployableUnitID]time.Duration, error)

// sleepFn has the same signature as time.Sleep(). Its purpose is to enable mocking that function
// from tests.
type sleepFn func(time.Duration)

// monitor watches the state of the given DeployableUnits and returns as soon as they seem to be
// up and running on their respective clusters.
//
// It does so by polling the clusters via kubectl every N seconds, and it prints out a status table
// on each iteration.
func (g *Goldpushk) monitor(ctx context.Context, units []DeployableUnit, getUptimes uptimesFn, sleep sleepFn) error {
	fmt.Printf("\nMonitoring the following services until they all reach %d seconds of uptime (polling every %d seconds):\n", g.minUptimeSeconds, g.uptimePollFrequencySeconds)
	for _, unit := range units {
		fmt.Printf("  %s\n", unit.CanonicalName())
	}

	if g.dryRun {
		fmt.Println("\nSkipping monitoring step (dry run).")
		return nil
	}

	fmt.Printf("\nWaiting %d seconds before starting the monitoring step.\n", int(delayBetweenPushAndMonitoring.Seconds()))
	sleep(delayBetweenPushAndMonitoring)

	// Estimate the width of the status table to print at each monitoring iteration. This table
	// consists of three columns: UPTIME, READY and NAME. The first two are both 10 characters wide.
	statusTableWidth := 20 // UPTIME + READY.
	// We determine the width of column NAME by looking for the longest name among all DeployableUnits
	// to monitor (e.g. "gold-skia-diffserver").
	longestName := 0
	for _, unit := range units {
		if len(unit.CanonicalName()) > longestName {
			longestName = len(unit.CanonicalName())
		}
	}
	statusTableWidth += longestName

	// Print status table header.
	w := tabwriter.NewWriter(os.Stdout, 10, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "\nUPTIME\tREADY\tNAME"); err != nil {
		return skerr.Wrap(err)
	}
	if err := w.Flush(); err != nil {
		return skerr.Wrap(err)
	}

	// Monitoring loop.
	for {
		// Get uptimes.
		uptimes, err := getUptimes(ctx, units, time.Now())
		if err != nil {
			return skerr.Wrap(err)
		}

		// Print out uptimes for each DeployableUnit.
		for _, unit := range units {
			// First we assume the DeployableUnit has no uptime. If it's not yet ready, it won't show up
			// in the uptimes dictionary.
			uptimeStr := "<None>"
			ready := "No"

			// We now check if it does have an uptime, and update the variables above accordingly if so.
			if t, ok := uptimes[unit.DeployableUnitID]; ok {
				uptimeStr = fmt.Sprintf("%ds", int64(t.Seconds()))
				if int(t.Seconds()) >= g.minUptimeSeconds {
					ready = "Yes"
				}
			}

			// Print out a row in the status table for the current DeployableUnit.
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", uptimeStr, ready, unit.CanonicalName()); err != nil {
				return skerr.Wrap(err)
			}
		}

		// Have all DeployableUnits been in the "ready" state for at least minUptimeSeconds?
		done := true
		for _, unit := range units {
			if t, ok := uptimes[unit.DeployableUnitID]; !ok || int(t.Seconds()) < g.minUptimeSeconds {
				done = false
				break
			}
		}

		// If so, break out of the monitoring loop.
		if done {
			if err := w.Flush(); err != nil {
				return skerr.Wrap(err)
			}
			return nil
		}

		// Otherwise, print a horizontal line to separate the uptimes from this iteration and the next.
		for i := 0; i < statusTableWidth; i++ {
			if _, err := fmt.Fprintf(w, "-"); err != nil {
				return skerr.Wrap(err)
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return skerr.Wrap(err)
		}
		if err := w.Flush(); err != nil {
			return skerr.Wrap(err)
		}

		// Wait before polling again.
		sleep(time.Duration(g.uptimePollFrequencySeconds) * time.Second)
	}
}

// getUptimes groups the given DeployableUnits by cluster, calls getUptimesSingleCluster once per
// cluster, and returns the union of the uptimes returned by both calls to getUptimesSingleCluster.
func (g *Goldpushk) getUptimes(ctx context.Context, units []DeployableUnit, now time.Time) (map[DeployableUnitID]time.Duration, error) {
	// Group units by cluster.
	publicUnits := []DeployableUnit{}
	corpUnits := []DeployableUnit{}
	for _, unit := range units {
		if unit.internal {
			corpUnits = append(corpUnits, unit)
		} else {
			publicUnits = append(publicUnits, unit)
		}
	}

	// This will hold the uptimes from both clusters.
	allUptimes := make(map[DeployableUnitID]time.Duration)

	// Once per cluster.
	for i := 0; i < 2; i++ {
		// Select the right cluster and DeployableUnits.
		cluster := clusterSkiaPublic
		units := publicUnits
		if i == 1 {
			cluster = clusterSkiaCorp
			units = corpUnits
		}

		// Skip if no units.
		if len(units) == 0 {
			continue
		}

		// Switch to the current cluster.
		if err := g.switchClusters(ctx, cluster); err != nil {
			return nil, skerr.Wrap(err)
		}

		// Get the uptimes for the current cluster.
		uptimes, err := g.getUptimesSingleCluster(ctx, units, now)
		if err != nil {
			return nil, skerr.Wrap(err)
		}

		// Add uptimes to the multicluster map.
		for unitID, uptime := range uptimes {
			allUptimes[unitID] = uptime
		}
	}

	return allUptimes, nil
}

// getUptimesSingleCluster takes a slice of DeployableUnits and returns a dictionary mapping
// DeployableUnitIDs to the time duration since all the pods corresponding to that unit have entered
// the "Ready" state.
//
// A DeployableUnit will not have a corresponding entry in the returned map if any of its pods are
// not yet ready, or if no matching pods are returned by kubectl.
//
// This method makes the following assumptions:
//   - All the given DeployableUnits belong to the same Kubernetes cluster.
//   - kubectl is already set up to operate on that cluster.
//   - A DeployableUnit may correspond to more than one pod (e.g. ReplicaSets).
func (g *Goldpushk) getUptimesSingleCluster(ctx context.Context, units []DeployableUnit, now time.Time) (map[DeployableUnitID]time.Duration, error) {
	// JSONPath expression to be passed to kubectl. Below is a sample fragment of what the output
	// looks like:
	//
	//   app:gold-chrome-gpu-baselineserver  podName:gold-chrome-gpu-baselineserver-5dfd8b65cb-9krrl  ready:True  readyLastTransitionTime:2019-10-03T16:45:48Z
	//   app:gold-chrome-gpu-baselineserver  podName:gold-chrome-gpu-baselineserver-5dfd8b65cb-hr86n  ready:True  readyLastTransitionTime:2019-09-30T13:20:39Z
	//   app:gold-chrome-gpu-baselineserver  podName:gold-chrome-gpu-baselineserver-5dfd8b65cb-l4lt5  ready:True  readyLastTransitionTime:2019-10-04T01:59:40Z
	//   app:gold-chrome-gpu-diffserver  podName:gold-chrome-gpu-diffserver-0  ready:True  readyLastTransitionTime:2019-10-09T18:43:08Z
	//   app:gold-chrome-gpu-ingestion-bt  podName:gold-chrome-gpu-ingestion-bt-f8b66844f-4969w  ready:True  readyLastTransitionTime:2019-10-04T01:54:54Z
	//   app:gold-chrome-gpu-skiacorrectness  podName:gold-chrome-gpu-skiacorrectness-67c547667d-cwt42  ready:True  readyLastTransitionTime:2019-10-04T02:01:11Z
	//
	// The output format should be fairly self explanatory, but to see an example of where those
	// are coming from, try running e.g. "kubectl get pod gold-skia-diffserver-0 -o json".
	//
	// Note: Field podName is not used, and is only included for debugging purposes. It will be
	// printed out to stdout if flag --logtostderr is passed.
	jsonPathExpr := `
{range .items[*]}
{'app:'}
{.metadata.labels.app}
{'  podName:'}
{.metadata.name}
{'  ready:'}
{.status.conditions[?(@.type == 'Ready')].status}
{'  readyLastTransitionTime:'}
{.status.conditions[?(@.type == 'Ready')].lastTransitionTime}
{'\n'}
{end}`
	jsonPathExpr = strings.ReplaceAll(jsonPathExpr, "\n", "")

	// Execute kubectl command that will return per-pod uptime.
	cmd := &exec.Command{
		Name: "kubectl",
		// See the comment above.
		Args:        []string{"get", "pods", "-o", fmt.Sprintf("jsonpath=%s", jsonPathExpr)},
		InheritPath: true,
		LogStderr:   true,
		LogStdout:   true,
	}
	stdout, err := exec.RunCommand(ctx, cmd)
	if err != nil {
		return nil, skerr.Wrapf(err, "failed to run %s", cmdToDebugStr(cmd))
	}

	// This map will hold the uptimes parsed from the command output.
	uptime := make(map[DeployableUnitID]time.Duration)

	// If at least one of the pods corresponding to a DeployableUnit is not ready, then it will be
	// excluded from the returned dictionary.
	//
	// Take for example the fictitious "kubectl get pods ..." command output below:
	//   ...
	//   app:gold-chrome-gpu-baselineserver  podName:gold-chrome-gpu-baselineserver-5dfd8b65cb-9krrl  ready:True  readyLastTransitionTime:2019-10-03T16:45:48Z
	//   app:gold-chrome-gpu-baselineserver  podName:gold-chrome-gpu-baselineserver-5dfd8b65cb-hr86n  ready:False  readyLastTransitionTime:2019-09-30T13:20:39Z
	//   app:gold-chrome-gpu-baselineserver  podName:gold-chrome-gpu-baselineserver-5dfd8b65cb-l4lt5  ready:True  readyLastTransitionTime:2019-10-04T01:59:40Z
	//   ...
	// In this example, DeployableUnit "gold-chrome-gpu-baselineserver" will be excluded from the
	// returned dictionary because one of its pods is not yet ready.
	//
	// The dictionary below keeps track of which DeployableUnits to exclude from this method's output.
	excludeFromOutput := make(map[DeployableUnitID]bool)

	// We will parse each output line using this regular expression.
	re := regexp.MustCompile(`app:(?P<app>\S*)\s+podName:(?P<podName>\S+)\s+ready:(?P<ready>\S+)\s+readyLastTransitionTime:(?P<readyLastTransitionTime>\S+)`)

	// Iterate over all output lines.
	for _, line := range strings.Split(stdout, "\n") {
		// Skip empty line at the end.
		if line == "" {
			continue
		}

		// Parse line, e.g. "app:gold-chrome-gpu-baselineserver  podName:gold-chrome-gpu-baselineserver-5dfd8b65cb-9krrl  ready:True  readyLastTransitionTime:2019-10-03T16:45:48Z"
		matches := re.FindStringSubmatch(line)

		// If for whatever reason the regular expression does not match, skip to the next line.
		if len(matches) < 4 {
			continue
		}

		// Extract values from current line.
		app := matches[1]                     // e.g. "gold-chrome-gpu-baselineserver"
		ready := matches[3]                   // e.g. "True"
		readyLastTransitionTime := matches[4] // e.g. "2019-10-03T16:45:48Z"

		// Iterate over the given DeployableUnits; see if there is a DeployableUnit that matches the
		// current line.
		var unitID DeployableUnitID
		for _, unit := range units {
			if unit.CanonicalName() == app {
				unitID = unit.DeployableUnitID
			}
		}

		// If no DeployableUnit matches, skip to the next line. This is OK since "kubectl get pods"
		// returns information about all pods running on the cluster, and not just the ones we are
		// interested in.
		if unitID == (DeployableUnitID{}) {
			continue
		}

		// If the pod is not yet ready, we exclude its corresponding DeployableUnit from the method's
		// output.
		if ready != "True" {
			delete(uptime, unitID) // Delete it from the output if it was previously added.
			excludeFromOutput[unitID] = true
			continue
		}

		// If the DeployableUnit has been excluded from the output due to another of its pods not being
		// ready, skip to the next line.
		if _, ok := excludeFromOutput[unitID]; ok {
			continue
		}

		// Parse the timestamp, e.g. "2019-09-30T13:20:33Z".
		t, err := time.Parse(kubectlTimestampLayout, readyLastTransitionTime)
		if err != nil {
			return nil, skerr.Wrap(err)
		}

		// Compute the time duration since the pod corresponding to the current line has been ready.
		readyFor := now.Sub(t)

		// We'll report the uptime of the pod that became ready the most recently.
		if currentMin, ok := uptime[unitID]; !ok || (readyFor < currentMin) {
			uptime[unitID] = readyFor
		}
	}

	return uptime, nil
}

// forAllDeployableUnits applies all deployable units (including canaried units)
// to the given function.
func (g *Goldpushk) forAllDeployableUnits(f func(unit DeployableUnit) error) error {
	for _, unit := range g.deployableUnits {
		if err := f(unit); err != nil {
			return skerr.Wrap(err)
		}
	}
	for _, unit := range g.canariedDeployableUnits {
		if err := f(unit); err != nil {
			return skerr.Wrap(err)
		}
	}
	return nil
}

// forAllGitRepos applies the *git.TempCheckouts for skia-{public,corp}-config to the given
// function.
func (g *Goldpushk) forAllGitRepos(f func(repo *git.TempCheckout, name string) error) error {
	if err := f(g.skiaPublicConfigCheckout, "skia-public-config"); err != nil {
		return skerr.Wrap(err)
	}
	if err := f(g.skiaCorpConfigCheckout, "skia-corp-config"); err != nil {
		return skerr.Wrap(err)
	}
	if err := f(g.k8sConfigCheckout, "k8s-config"); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// cmdToDebugStr returns a human-readable string representation of an *exec.Command.
func cmdToDebugStr(cmd *exec.Command) string {
	return fmt.Sprintf("%s %s", cmd.Name, strings.Join(cmd.Args, " "))
}
