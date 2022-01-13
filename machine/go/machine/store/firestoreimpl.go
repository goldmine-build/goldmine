package store

import (
	"context"
	"math/rand"
	"strings"
	"time"

	gcfirestore "cloud.google.com/go/firestore"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/firestore"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/machine/go/machine"
	"go.skia.org/infra/machine/go/machineserver/config"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	machinesCollectionName = "machines"

	appName = "machineserver"

	updateTimeout = 10 * time.Second

	updateRetries = 5
)

var (
	// The amount of time, in seconds, at most, before retrying the query in Watch.
	watchRecoverBackoff int64 = 6
)

// FirestoreImpl implements the Store interface.
type FirestoreImpl struct {
	firestoreClient    *firestore.Client
	machinesCollection *gcfirestore.CollectionRef

	updateCounter                            metrics2.Counter
	updateDataToErrorCounter                 metrics2.Counter
	watchReceiveSnapshotCounter              metrics2.Counter
	watchDataToErrorCounter                  metrics2.Counter
	getCounter                               metrics2.Counter
	listCounter                              metrics2.Counter
	deleteCounter                            metrics2.Counter
	listPowerCycleCounter                    metrics2.Counter
	listPowerCycleIterErrorCounter           metrics2.Counter
	listIterFailureCounter                   metrics2.Counter
	watchForPowerCycleReceiveSnapshotCounter metrics2.Counter
	watchForPowerCycleDataToErrorCounter     metrics2.Counter
}

// storeDescription is how machine.Description is mapped into firestore.
type storeDescription struct {
	// Mode describes if the machine is capable of running tasks or is otherwise not ready.
	Mode machine.Mode

	// AttachedDevice is the kind of device attached.
	AttachedDevice machine.AttachedDevice

	// Annotation is used to record the most recent non-user change to Description.
	Annotation fsAnnotation

	// Note is a user authored message on the state of a machine.
	Note fsAnnotation

	// Version of test_machine_monitor being run.
	Version string

	// PowerCycle is true if the machine needs to be power-cycled.
	PowerCycle bool

	// LastUpdated is the timestamp that the machine last checked in.
	LastUpdated         time.Time
	RunningSwarmingTask bool
	LaunchedSwarming    bool      // True if test_machine_monitor launched Swarming.
	RecoveryStart       time.Time // When did the machine start being in recovery mode.

	// Battery, Temperature, DeviceUptime refer to the attached Android device, if any.
	Battery      int                // Charge as an integer percent, e.g. 50% = 50.
	Temperature  map[string]float64 // In Celsius.
	DeviceUptime int32              // Seconds

	// SSHUserIP, for example, "root@skia-sparky360-03" indicates we should connect to the
	// given ChromeOS device at that username and ip/hostname.
	SSHUserIP string

	// SuppliedDimensions are any dimensions supplied by a human that should be merged with
	// the existing dimensions. These are useful for ChromeOS devices, where gathering things
	// like GPU and CPU can be tough from the CLI.
	SuppliedDimensions machine.SwarmingDimensions

	// Dimensions describe the machine and what tasks it is capable of running.
	Dimensions machine.SwarmingDimensions

	// OS, DeviceType, and Quarantined are mirrored out of Dimensions, so we can query them.
	OS          []string
	DeviceType  []string
	Quarantined []string
}

// fsAnnotation models how machine.Annotation is stored in Firestore. This serves to
// decouple the schema stored in FS from the schema used elsewhere.
type fsAnnotation struct {
	Message   string
	User      string
	Timestamp time.Time
}

// NewFirestoreImpl returns a new instance of FirestoreImpl that is backed by Firestore.
func NewFirestoreImpl(ctx context.Context, local bool, instanceConfig config.InstanceConfig) (*FirestoreImpl, error) {
	ts, err := auth.NewDefaultTokenSource(local, "https://www.googleapis.com/auth/datastore")
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to create tokensource.")
	}

	firestoreClient, err := firestore.NewClient(ctx, instanceConfig.Store.Project, appName, instanceConfig.Store.Instance, ts)
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to create firestore client for app: %q instance: %q", appName, instanceConfig.Store.Instance)
	}
	return &FirestoreImpl{
		firestoreClient:                          firestoreClient,
		machinesCollection:                       firestoreClient.Collection(machinesCollectionName),
		updateCounter:                            metrics2.GetCounter("machine_store_update"),
		updateDataToErrorCounter:                 metrics2.GetCounter("machine_store_update_datato_error"),
		watchReceiveSnapshotCounter:              metrics2.GetCounter("machine_store_watch_receive_snapshot"),
		watchDataToErrorCounter:                  metrics2.GetCounter("machine_store_watch_datato_error"),
		getCounter:                               metrics2.GetCounter("machine_store_get"),
		listCounter:                              metrics2.GetCounter("machine_store_list"),
		listPowerCycleCounter:                    metrics2.GetCounter("machine_store_list_power_cycle"),
		listPowerCycleIterErrorCounter:           metrics2.GetCounter("machine_store_list_power_cycle_iter_error"),
		deleteCounter:                            metrics2.GetCounter("machine_store_delete"),
		listIterFailureCounter:                   metrics2.GetCounter("machine_store_list_iter_error"),
		watchForPowerCycleReceiveSnapshotCounter: metrics2.GetCounter("machine_store_watch_for_power_cycle_receive_snapshot"),
		watchForPowerCycleDataToErrorCounter:     metrics2.GetCounter("machine_store_watch_for_power_cycle_datato_error"),
	}, nil
}

// Get implements the Store interface.
func (st *FirestoreImpl) Get(ctx context.Context, machineID string) (machine.Description, error) {
	st.getCounter.Inc(1)
	ret := machine.NewDescription(ctx)
	snap, err := st.machinesCollection.Doc(machineID).Get(ctx)
	if err != nil {
		return ret, skerr.Wrapf(err, "Failed to query for machine: %q", machineID)
	}
	if err := snap.DataTo(&ret); err != nil {
		return ret, skerr.Wrapf(err, "Failed to deserialize for machine: %q", machineID)
	}
	return ret, nil
}

// Update implements the Store interface.
func (st *FirestoreImpl) Update(ctx context.Context, machineID string, updateCallback UpdateCallback) error {
	st.updateCounter.Inc(1)
	docRef := st.machinesCollection.Doc(machineID)
	return st.firestoreClient.RunTransaction(ctx, "store", "update", updateRetries, updateTimeout, func(ctx context.Context, tx *gcfirestore.Transaction) error {
		var storeDescription storeDescription
		machineDescription := machine.NewDescription(ctx)
		machineDescription.Dimensions[machine.DimID] = []string{machineID}
		if snap, err := tx.Get(docRef); err == nil {
			if err := snap.DataTo(&storeDescription); err != nil {
				st.updateDataToErrorCounter.Inc(1)
				return skerr.Wrapf(err, "Failed to deserialize firestore Get response for %q", machineID)
			}
			machineDescription = convertFSDescription(storeDescription)
		} else if st, ok := status.FromError(err); ok && st.Code() != codes.NotFound {
			return skerr.Wrapf(err, "Failed querying firestore for %q", machineID)
		}

		updatedMachineDescription := updateCallback(machineDescription)
		updatedStoreDescription := convertDescription(updatedMachineDescription)

		return tx.Set(docRef, &updatedStoreDescription)
	})
}

// Watch implements the Store interface.
func (st *FirestoreImpl) Watch(ctx context.Context, machineID string) <-chan machine.Description {
	iter := st.machinesCollection.Doc(machineID).Snapshots(ctx)
	ch := make(chan machine.Description)
	go func() {
		for {
			snap, err := iter.Next()
			if err != nil {
				if ctx.Err() == context.Canceled {
					sklog.Warningf("Context canceled; closing channel: %s", err)
				} else if stErr, ok := status.FromError(err); ok && stErr.Code() == codes.Canceled {
					sklog.Warningf("Context canceled; closing channel: %s", err)
				} else {
					iter.Stop()
					time.Sleep(time.Second * time.Duration(rand.Int63n(watchRecoverBackoff)))
					iter = st.machinesCollection.Doc(machineID).Snapshots(ctx)
					sklog.Warningf("iter returned error; retrying query: %s", err)
					continue
				}
				iter.Stop()
				close(ch)
				return
			}
			if !snap.Exists() {
				continue
			}
			var storeDescription storeDescription
			if err := snap.DataTo(&storeDescription); err != nil {
				sklog.Errorf("Failed to read data from snapshot: %s", err)
				st.watchDataToErrorCounter.Inc(1)
				continue
			}
			machineDescription := convertFSDescription(storeDescription)
			st.watchReceiveSnapshotCounter.Inc(1)
			ch <- machineDescription
		}
	}()
	return ch
}

// WatchForPowerCycle implements the Store interface.
func (st *FirestoreImpl) WatchForPowerCycle(ctx context.Context, rack string) <-chan string {
	q := st.machinesCollection.Where("PowerCycle", "==", true).Where("RunningSwarmingTask", "==", false)
	ch := make(chan string)
	go func() {
		defer close(ch)
		for qsnap := range firestore.QuerySnapshotChannel(ctx, q) {
			for {
				snap, err := qsnap.Documents.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					sklog.Errorf("Failed to read document snapshot: %s", err)
					continue
				}
				var storeDescription storeDescription
				if err := snap.DataTo(&storeDescription); err != nil {
					sklog.Errorf("Failed to read data from snapshot: %s", err)
					st.watchForPowerCycleDataToErrorCounter.Inc(1)
					continue
				}
				machineDescription := convertFSDescription(storeDescription)
				machineID := machineDescription.Dimensions[machine.DimID][0]
				// If rack is set then only respond to powercycle events for that rack.
				if rack != "" && !strings.Contains(machineID, rack) {
					continue
				}
				st.watchForPowerCycleReceiveSnapshotCounter.Inc(1)
				err = st.Update(ctx, machineID, func(previous machine.Description) machine.Description {
					ret := previous.Copy()
					ret.PowerCycle = false
					return ret
				})
				if err != nil {
					sklog.Errorf("Failed to update machine.Description PowerCycle: %s", err)
					// Just log the error, still powercycle the machine.
				}
				ch <- machineID
			}
		}
	}()
	return ch
}

// ListPowerCycle implements the Store interface.
func (st *FirestoreImpl) ListPowerCycle(ctx context.Context) ([]string, error) {
	st.listPowerCycleCounter.Inc(1)
	ret := []string{}
	iter := st.machinesCollection.Where("PowerCycle", "==", true).Documents(ctx)
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			st.listPowerCycleIterErrorCounter.Inc(1)
			return nil, skerr.Wrapf(err, "ListPowerCycle failed to read description.")
		}

		var storeDescription storeDescription
		if err := snap.DataTo(&storeDescription); err != nil {
			st.listPowerCycleIterErrorCounter.Inc(1)
			sklog.Errorf("ListPowerCycle failed to read data from snapshot: %s", err)
			continue
		}
		machineDescription := convertFSDescription(storeDescription)
		ret = append(ret, machineDescription.Dimensions[machine.DimID][0])
	}
	return ret, nil
}

// List implements the Store interface.
func (st *FirestoreImpl) List(ctx context.Context) ([]machine.Description, error) {
	st.listCounter.Inc(1)
	ret := []machine.Description{}
	iter := st.machinesCollection.Documents(ctx)
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			st.listIterFailureCounter.Inc(1)
			return nil, skerr.Wrapf(err, "List failed to read description.")
		}

		var storeDescription storeDescription
		if err := snap.DataTo(&storeDescription); err != nil {
			st.listIterFailureCounter.Inc(1)
			sklog.Errorf("Failed to read data from snapshot: %s", err)
			continue
		}
		machineDescription := convertFSDescription(storeDescription)
		ret = append(ret, machineDescription)
	}
	return ret, nil
}

// Delete implements the Store interface.
func (st *FirestoreImpl) Delete(ctx context.Context, machineID string) error {
	st.deleteCounter.Inc(1)

	_, err := st.machinesCollection.Doc(machineID).Delete(ctx)
	return err
}

func convertDescription(m machine.Description) storeDescription {
	return storeDescription{
		Annotation:          convertAnnotation(m.Annotation),
		AttachedDevice:      forceToAttachedDevice(m.AttachedDevice),
		Battery:             m.Battery,
		DeviceType:          m.Dimensions[machine.DimDeviceType],
		DeviceUptime:        m.DeviceUptime,
		Dimensions:          m.Dimensions,
		LastUpdated:         m.LastUpdated,
		LaunchedSwarming:    m.LaunchedSwarming,
		Mode:                m.Mode,
		Note:                convertAnnotation(m.Note),
		OS:                  m.Dimensions[machine.DimOS],
		PowerCycle:          m.PowerCycle,
		Quarantined:         m.Dimensions[machine.DimQuarantined],
		RecoveryStart:       m.RecoveryStart,
		RunningSwarmingTask: m.RunningSwarmingTask,
		SSHUserIP:           m.SSHUserIP,
		SuppliedDimensions:  m.SuppliedDimensions,
		Temperature:         m.Temperature,
		Version:             m.Version,
	}
}

func convertAnnotation(a machine.Annotation) fsAnnotation {
	return fsAnnotation{
		Message:   a.Message,
		User:      a.User,
		Timestamp: a.Timestamp,
	}
}

func convertFSAnnotation(a fsAnnotation) machine.Annotation {
	return machine.Annotation{
		Message:   a.Message,
		User:      a.User,
		Timestamp: a.Timestamp,
	}
}

func forceToAttachedDevice(a machine.AttachedDevice) machine.AttachedDevice {
	for _, d := range machine.AllAttachedDevices {
		if a == d {
			return a
		}
	}
	return machine.AttachedDeviceNone
}

// convertFSDescription converts the firestore version of the description to the common format.
func convertFSDescription(s storeDescription) machine.Description {
	return machine.Description{
		Annotation:          convertFSAnnotation(s.Annotation),
		AttachedDevice:      forceToAttachedDevice(s.AttachedDevice),
		Battery:             s.Battery,
		DeviceUptime:        s.DeviceUptime,
		Dimensions:          s.Dimensions,
		LastUpdated:         s.LastUpdated,
		LaunchedSwarming:    s.LaunchedSwarming,
		Mode:                s.Mode,
		Note:                convertFSAnnotation(s.Note),
		PowerCycle:          s.PowerCycle,
		RecoveryStart:       s.RecoveryStart,
		RunningSwarmingTask: s.RunningSwarmingTask,
		SSHUserIP:           s.SSHUserIP,
		SuppliedDimensions:  s.SuppliedDimensions,
		Temperature:         s.Temperature,
		Version:             s.Version,
	}
}

// Affirm that FirestoreImpl implements the Store interface.
var _ Store = (*FirestoreImpl)(nil)
