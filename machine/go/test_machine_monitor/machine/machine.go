// Package machine is for interacting with the machine state server. See //machine.
package machine

import (
	"context"
	"os"
	"sync"
	"time"

	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/timer"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/machine/go/machine"
	"go.skia.org/infra/machine/go/machine/sink"
	"go.skia.org/infra/machine/go/machine/store"
	"go.skia.org/infra/machine/go/machineserver/config"
	"go.skia.org/infra/machine/go/test_machine_monitor/adb"
	"go.skia.org/infra/machine/go/test_machine_monitor/swarming"
)

const (
	interrogateDuration = 30 * time.Second
)

// Machine is the interface to the machine state server. See //machine.
type Machine struct {
	// store is how we get our dimensions and status updates from the machine state server.
	store store.Store

	// sink is how we send machine.Events to the the machine state server.
	sink sink.Sink

	// adb makes calls to the adb server.
	adb adb.Adb

	// MachineID is the swarming id of the machine.
	MachineID string

	// Hostname is the hostname(), which is the pod name under k8s.
	Hostname string

	// KubernetesImage is the container image being run.
	KubernetesImage string

	// Version of test_machine_monitor being run.
	Version string

	// startTime is the time when this machine started running.
	startTime time.Time

	// Metrics
	interrogateTimer           metrics2.Float64SummaryMetric
	interrogateAndSendFailures metrics2.Counter
	storeWatchArrivalCounter   metrics2.Counter

	// startSwarming is true if test_machine_monitor was used to launch Swarming.
	startSwarming bool

	// runningTask is true if the machine is currently running a swarming task.
	runningTask bool

	// mutex protects the description due to the fact it will be updated asynchronously via
	// the firestore snapshot query.
	mutex sync.Mutex

	// description is provided by the machine state server. This tells us what
	// to tell swarming, what our current mode is, etc.
	description machine.Description
}

// New return an instance of *Machine.
func New(ctx context.Context, local bool, instanceConfig config.InstanceConfig, startTime time.Time, version string, startSwarming bool) (*Machine, error) {
	store, err := store.New(ctx, false, instanceConfig)
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to build store instance.")
	}
	sink, err := sink.New(ctx, local, instanceConfig)
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to build sink instance.")
	}

	kubernetesImage := os.Getenv(swarming.KubernetesImageEnvVar)
	hostname, err := os.Hostname()
	if err != nil {
		return nil, skerr.Wrapf(err, "Could not determine hostname.")
	}
	machineID := os.Getenv(swarming.SwarmingBotIDEnvVar)
	if machineID == "" {
		// Fall back to hostname so we can track machines that
		// test_machine_monitor is running on that don't also run Swarming.
		machineID = hostname
	}

	return &Machine{
		store:                      store,
		sink:                       sink,
		adb:                        adb.New(),
		MachineID:                  machineID,
		Hostname:                   hostname,
		KubernetesImage:            kubernetesImage,
		Version:                    version,
		startTime:                  startTime,
		startSwarming:              startSwarming,
		interrogateTimer:           metrics2.GetFloat64SummaryMetric("bot_config_machine_interrogate_timer", map[string]string{"machine": machineID}),
		interrogateAndSendFailures: metrics2.GetCounter("bot_config_machine_interrogate_and_send_errors", map[string]string{"machine": machineID}),
		storeWatchArrivalCounter:   metrics2.GetCounter("bot_config_machine_store_watch_arrival", map[string]string{"machine": machineID}),
	}, nil
}

// interrogate the machine we are running on for state-related information. It compiles that into
// a machine.Event and returns it.
func (m *Machine) interrogate(ctx context.Context) machine.Event {
	defer timer.NewWithSummary("interrogate", m.interrogateTimer).Stop()

	ret := machine.NewEvent()
	ret.Host.Name = m.MachineID
	ret.Host.PodName = m.Hostname
	ret.Host.KubernetesImage = m.KubernetesImage
	ret.Host.Version = m.Version
	ret.Host.StartTime = m.startTime
	ret.RunningSwarmingTask = m.runningTask
	ret.LaunchedSwarming = m.startSwarming

	if ce, ok := m.tryInterrogatingChromeOSDevice(ctx); ok {
		sklog.Infof("Successful communication with ChromeOS device: %#v", ce)
		ret.ChromeOS = ce
	} else if ae, ok := m.tryInterrogatingAndroidDevice(ctx); ok {
		sklog.Infof("Successful communication with Android device: %#v", ae)
		ret.Android = ae
	} else if ie, ok := m.tryInterrogatingIOSDevice(ctx); ok {
		sklog.Infof("Successful communication with iOS device: %#v", ie)
		ret.IOS = ie
	} else {
		sklog.Infof("No attached device found")
	}

	return ret
}

// interrogateAndSend gathers the state for this machine and sends it to the sink. Of note, this
// does not directly determine what dimensions this machine should have. The machine server that
// listens to the events will determine the dimensions based on the reported state and any
// information it has from other sources (e.g. human-supplied details, previously attached devices)
func (m *Machine) interrogateAndSend(ctx context.Context) error {
	event := m.interrogate(ctx)
	if err := m.sink.Send(ctx, event); err != nil {
		return skerr.Wrapf(err, "Failed to send interrogation step.")
	}
	return nil
}

// Start the background processes that send events to the sink and watch for
// firestore changes.
func (m *Machine) Start(ctx context.Context) error {
	if err := m.interrogateAndSend(ctx); err != nil {
		return skerr.Wrap(err)
	}

	// Start a loop that scans for local devices and sends pubsub events with all the
	// data every 30s.
	go util.RepeatCtx(ctx, interrogateDuration, func(ctx context.Context) {
		if err := m.interrogateAndSend(ctx); err != nil {
			m.interrogateAndSendFailures.Inc(1)
			sklog.Errorf("interrogateAndSend failed: %s", err)
		}
	})

	m.startStoreWatch(ctx)
	return nil
}

// startStoreWatch starts a loop that does a firestore onsnapshot watcher
// that gets the dims and state we should be reporting to swarming.
func (m *Machine) startStoreWatch(ctx context.Context) {
	go func() {
		for desc := range m.store.Watch(ctx, m.MachineID) {
			m.storeWatchArrivalCounter.Inc(1)
			m.UpdateDescription(desc)
		}
	}()
}

// UpdateDescription applies any change in behavior based on the new given description. This
// impacts what we tell Swarming, what mode we are in, if we should communicate with a device
// via SSH, etc.
func (m *Machine) UpdateDescription(desc machine.Description) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.description = desc
}

// DimensionsForSwarming returns the dimensions that should be reported to swarming.
func (m *Machine) DimensionsForSwarming() machine.SwarmingDimensions {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.description.Dimensions
}

// GetMaintenanceMode returns true if the machine should report to Swarming that it is
// in maintenance mode. Swarming does not have a "recovery" mode, so we group that in.
func (m *Machine) GetMaintenanceMode() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.description.Mode == machine.ModeRecovery || m.description.Mode == machine.ModeMaintenance
}

// SetIsRunningSwarmingTask records if a swarming task is being run.
func (m *Machine) SetIsRunningSwarmingTask(isRunning bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.runningTask = isRunning
}

// IsRunningSwarmingTask returns true is a swarming task is currently running.
func (m *Machine) IsRunningSwarmingTask() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.runningTask
}

// RebootDevice reboots the attached device.
func (m *Machine) RebootDevice(ctx context.Context) error {
	m.mutex.Lock()
	shouldReboot := len(m.description.Dimensions[machine.DimAndroidDevices]) > 0
	m.mutex.Unlock()

	if shouldReboot {
		return m.adb.Reboot(ctx)
	}
	sklog.Info("No attached device to reboot.")
	return nil
}

// tryInterrogatingAndroidDevice attempts to communicate with an Android device using the
// adb interface. If there is one attached, this function returns true and the information gathered
// (which can be partially filled out). If there is not a device attached, false is returned.
func (m *Machine) tryInterrogatingAndroidDevice(ctx context.Context) (machine.Android, bool) {
	var ret machine.Android
	if uptime, err := m.adb.Uptime(ctx); err != nil {
		sklog.Warningf("Failed to read uptime - assuming there is no device attached: %s", err)
		return ret, false // Assume there is no Android device attached.
	} else {
		ret.Uptime = uptime
	}

	if props, err := m.adb.RawProperties(ctx); err != nil {
		sklog.Warningf("Failed to read android properties: %s", err)
	} else {
		ret.GetProp = props
	}

	if battery, err := m.adb.RawDumpSys(ctx, "battery"); err != nil {
		sklog.Warningf("Failed to read android battery status: %s", err)
	} else {
		ret.DumpsysBattery = battery
	}

	if thermal, err := m.adb.RawDumpSys(ctx, "thermalservice"); err != nil {
		sklog.Warningf("Failed to read android thermal status: %s", err)
	} else {
		ret.DumpsysThermalService = thermal
	}
	return ret, true
}

func (m *Machine) tryInterrogatingIOSDevice(_ context.Context) (machine.IOS, bool) {
	// TODO(erikrose)
	return machine.IOS{}, false
}

func (m *Machine) tryInterrogatingChromeOSDevice(_ context.Context) (machine.ChromeOS, bool) {
	// TODO(kjlubick)
	return machine.ChromeOS{}, false
}
