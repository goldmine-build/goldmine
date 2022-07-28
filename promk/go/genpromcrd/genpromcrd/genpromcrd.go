// Package genpromcrd implements all the functionality for the genpromcrd
// command line application.
package genpromcrd

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"

	"go.skia.org/infra/go/kube/clusterconfig"
	"go.skia.org/infra/go/prom/crd"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/sklog/nooplogging"
	"go.skia.org/infra/go/sklog/sklogimpl"
	"go.skia.org/infra/go/sklog/stdlogging"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/k8s-checker/go/k8s_config"
	yaml "gopkg.in/yaml.v2"
)

// podMonitoring is a template for how an appgroup should be scraped by Managed
// Promenteus.
const podMonitoring = `apiVersion: monitoring.googleapis.com/v1
kind: PodMonitoring
metadata:
 name: {{ .AppGroup }}-{{ .Namespace }}
spec:
 selector:
   matchLabels:
      appgroup: {{ .AppGroup }}
 endpoints:
   - port: prom
     interval: 15s
 targetLabels:
   fromPod:
     - from: app
     - from: appgroup
`

// podMonitoringTemplate is the compiled podMonitoring template.
var podMonitoringTemplate = template.Must(template.New("podMonitoring").Parse(podMonitoring))

// AlertTarget represents a single appgroup that might need monitoring.
type AlertTarget struct {
	// AppGroup is the value of the template.label.appgroup for the pods to be monitored.
	AppGroup string

	// Namespace the pods are running in.
	Namespace string

	// Directory where the YAML file was found for this appgroup. The scraping
	// and alerting file will be writtin back into this directory.
	Directory string
}

// TargetFilename is the absolute filename where the pod scraping and alert
// rules should be written as YAML.
func (a AlertTarget) TargetFilename() string {
	return filepath.Join(a.Directory, fmt.Sprintf("%s_%s_appgroup_alerts.yml", a.AppGroup, a.Namespace))
}

// PodMonitoring is a YAML CRD of how the pods should be scraped.
func (a AlertTarget) PodMonitoring() (string, error) {
	var out bytes.Buffer
	if err := podMonitoringTemplate.Execute(&out, a); err != nil {
		return "", skerr.Wrapf(err, "Failed to write PodMonitoring for %v", a)
	}
	return out.String(), nil
}

// AlertTargets keeps track of multiple found AlertTarget's, de-duplicating
// AlertTargets that are the same.
type AlertTargets map[AlertTarget]bool

// NamespaceOrDefault returns "default" if the empty string is passed in as a
// namespace.
func NamespaceOrDefault(ns string) string {
	if ns == "" {
		return "default"
	}
	return ns
}

// The possible file extensions used for YAML files.
var yamlFileExtensions = []string{".yaml", ".yml"}

// getAlertTargetsFromFilename parses the given file and for each Deployment or
// StatefulSet found in the file will return an AlertTarget for each one found
// that has an `appgroup` label.
func getAlertTargetsFromFilename(filename string) (AlertTargets, error) {
	ret := AlertTargets{}
	err := util.WithReadFile(filename, func(f io.Reader) error {
		b, err := ioutil.ReadAll(f)
		if err != nil {
			return err
		}
		deployments, statefulSets, _, err := k8s_config.ParseK8sConfigFile(b)
		if err != nil {
			return skerr.Wrapf(err, "failed to parse")
		}
		for _, d := range deployments {
			if appgroup, ok := d.Spec.Template.Labels["appgroup"]; ok {
				ret[AlertTarget{
					AppGroup:  appgroup,
					Namespace: NamespaceOrDefault(d.Namespace),
					Directory: filepath.Dir(filename),
				}] = true
			}
		}
		for _, d := range statefulSets {
			if appgroup, ok := d.Spec.Template.Labels["appgroup"]; ok {
				ret[AlertTarget{
					AppGroup:  appgroup,
					Namespace: NamespaceOrDefault(d.Namespace),
					Directory: filepath.Dir(filename),
				}] = true
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return ret, nil
}

// getAllAlertTargetsUnderDir walks the given directory tree and applies
// getAlertTargetsFromFilename to each file and returns all the collected
// AlertTarget's.
//
// getAllAlertTargetsUnderDir will only look in sub-directories that correspond
// to cluster names.
func getAllAlertTargetsUnderDir(root string) (AlertTargets, error) {
	ret := AlertTargets{}

	// Load up the cluster config so we can use the cluster names
	// to know which sub-directories of the git repo we should
	// process.
	clusters, err := clusterconfig.NewFromEmbeddedConfig()
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	for clusterName := range clusters.Clusters {
		dir := filepath.Join(root, clusterName)
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			sklog.Infof("Skipping cluster as the corresponding directory does not exist: %q", dir)
			continue
		}

		fileSystem := os.DirFS(dir)
		err = fs.WalkDir(fileSystem, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !util.In(filepath.Ext(path), yamlFileExtensions) {
				return nil
			}
			alertTargets, err := getAlertTargetsFromFilename(filepath.Join(dir, path))
			if err != nil {
				sklog.Errorf("Failed to read file: %s", err)
				return nil
			}
			for key := range alertTargets {
				ret[key] = true
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return ret, nil
}

// App is the application.
type App struct {
	directory string
	logging   bool
	dryrun    bool
}

// NewApp returns a new *App.
func NewApp() *App {
	return &App{}
}

// flagSet returns a flag.FlagSet for the App.
func (a *App) flagSet() *flag.FlagSet {
	ret := flag.NewFlagSet("genpromcmd", flag.ExitOnError)
	ret.StringVar(&(a.directory), "directory", "", "The directory that contains a checkout of k8s-config.")
	ret.BoolVar(&(a.logging), "logtostdout", false, "If true then write logging on stdout.")
	ret.BoolVar(&(a.dryrun), "dryrun", false, "If true then just print the names of the files that would be written.")
	ret.Usage = func() {

		fmt.Printf("usage: genpromcrd --directory=[k8s-config checkout dir] [options]\n")
		fmt.Printf("options:\n")
		ret.PrintDefaults()

		usage := `
The genpromcrd cmd runs over all Deployments and StatefulSets and
writes out Managed Prometheus CRDs for both scraping and alerting.
For example, given the following file in the git repo that contains
all the cluster config:

	k8s-config/
	├── monitoring
	│   └── appgroups
	│       └── perf.yml
	└── skia-infra-public
	    └── perf.yml

All the Rules files for alerts to run for all Deployments and
StatefulSets are held under /monitoring/appgroups and the name
of the file before the '.yml' corresponds to an appgroup label.

Since perf.yaml resides inside a directory associated with a
cluster, the Deployment there runs in the namespace 'somenamespace',
and has .template.label.appgroup=perf, a new file will be written to:

   skia-infra-public/perf_somenamespace_appgroup_alerts.yml

which is a modified version of /monitoring/appgroups/perf.yaml, updated
to scrape the deployment in the correct namespace, and it will also
contain 'absent()' alerts for all the alerts defined in 'perf.yml'.

The list of directories processed are defined in:

    //kube/clusters/config.json

`
		fmt.Println(usage)
	}

	return ret
}

// findRulesForAppGroup returns a parsed crd.Rules for the given appgroup if one
// exists, otherwise it returns an error.
func (a *App) findRulesForAppGroup(appgroup string) (*crd.Rules, error) {
	filename := filepath.Join(a.directory, "monitoring", "appgroups", appgroup+".yml")
	var out crd.Rules

	err := util.WithReadFile(filename, func(f io.Reader) error {
		if err := yaml.NewDecoder(f).Decode(&out); err != nil {
			return skerr.Wrapf(err, "Failed to read rules file: %q", filename)
		}
		return nil
	})
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to open %q: %s", filename, err)
	}
	return &out, nil
}

// Main is the application main entry point.
//
// Args are the cli arguments, should be passed in as os.Args.
func (a *App) Main(args []string) error {
	if err := a.flagSet().Parse(args[1:]); err != nil {
		return skerr.Wrapf(err, "Failed to parse flags")
	}

	if a.logging {
		sklogimpl.SetLogger(stdlogging.New(os.Stdout))
	} else {
		sklogimpl.SetLogger(nooplogging.New())
	}

	if a.directory == "" {
		return skerr.Fmt("--directory must be specified.")
	}

	absDirectory, err := filepath.Abs(a.directory)
	if err != nil {
		return skerr.Wrapf(err, "Can't make --directory value into an absoute path.")
	}
	allAppGroups, err := getAllAlertTargetsUnderDir(absDirectory)
	if err != nil {
		return skerr.Wrapf(err, "Failed parsing Deployments and StatefulSets.")
	}

	// Write CRDs for each appgroup.
	for appGroup := range allAppGroups {
		// Open and parse as Rules if it exists.
		rules, err := a.findRulesForAppGroup(appGroup.AppGroup)
		if err != nil {
			// Just information because we expect that not all pods will use
			// genpromcrd for controlling scraping and alerting.
			sklog.Infof("Failed to find appgroup: %s", err)
			continue
		}

		// Add in absent versions of rules.
		rules.AddAbsentRules()

		// Add Namespace
		rules.MetaData.Namespace = appGroup.Namespace

		// Write out the CRDs.
		serializeRules, err := yaml.Marshal(rules)
		if err != nil {
			return skerr.Wrapf(err, "Failed to marshall new Rules into YAML for %v", appGroup)
		}
		serializedPodMonitoring, err := appGroup.PodMonitoring()
		if err != nil {
			return skerr.Wrapf(err, "Failed to write new PodMontoring into YAML for %v", appGroup)
		}
		if a.dryrun {
			fmt.Println(appGroup.TargetFilename())
			continue
		}
		err = util.WithWriteFile(appGroup.TargetFilename(), func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "%s\n---\n%s", serializeRules, serializedPodMonitoring)
			return err
		})
		if err != nil {
			return skerr.Wrapf(err, "Failed to write file for %v", appGroup)
		}
		sklog.Infof("Processed %v", appGroup)
	}
	return nil
}
