package store

import (
	"context"
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

// StoreImpl implements the Store interface.
type StoreImpl struct {
	firestoreClient    *firestore.Client
	machinesCollection *gcfirestore.CollectionRef

	updateCounter                               metrics2.Counter
	updateDataToErrorCounter                    metrics2.Counter
	watchReceiveSnapshotCounter                 metrics2.Counter
	watchDataToErrorCounter                     metrics2.Counter
	listCounter                                 metrics2.Counter
	deleteCounter                               metrics2.Counter
	listIterFailureCounter                      metrics2.Counter
	watchForDeletablePodsReceiveSnapshotCounter metrics2.Counter
	watchForDeletablePodsDataToErrorCounter     metrics2.Counter
	watchForPowerCycleReceiveSnapshotCounter    metrics2.Counter
	watchForPowerCycleDataToErrorCounter        metrics2.Counter
}

// storeDescription is how machine.Description is mapped into firestore.
//
// Some fields from machine.Description are mirrored to top level
// storeDescription fields so we can query on them.
type storeDescription struct {
	// OS is a mirror of MachineDescription.Dimensions["os"].
	OS []string

	// OS is a mirror of MachineDescription.Dimensions["device_type"].
	DeviceType []string

	// OS is a mirror of MachineDescription.Dimensions["quarantined"].
	Quarantined []string

	// Mode is a mirror of MachineDescription.Mode.
	Mode machine.Mode

	// LastUpdated is a mirror of MachineDescription.LastUpdated.
	LastUpdated time.Time

	// ScheduledForDeletion is a mirror of MachineDescription.ScheduledForDeletion.
	ScheduledForDeletion string

	// RunningSwarmingTask is a mirror of MachineDescription.RunningSwarmingTask.
	RunningSwarmingTask bool

	// PowerCycle is a mirror of MachineDescription.PowerCycle.
	PowerCycle bool

	// MachineDescription is the full machine.Description. The values that are
	// mirrored to fields of storeDescription are still fully stored here and
	// are considered the source of truth.
	MachineDescription machine.Description
}

// New returns a new instance of StoreImpl.
func New(ctx context.Context, local bool, instanceConfig config.InstanceConfig) (*StoreImpl, error) {
	ts, err := auth.NewDefaultTokenSource(local, "https://www.googleapis.com/auth/datastore")
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to create tokensource.")
	}

	firestoreClient, err := firestore.NewClient(ctx, instanceConfig.Store.Project, appName, instanceConfig.Store.Instance, ts)
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to create firestore client for app: %q instance: %q", appName, instanceConfig.Store.Instance)
	}
	return &StoreImpl{
		firestoreClient:                             firestoreClient,
		machinesCollection:                          firestoreClient.Collection(machinesCollectionName),
		updateCounter:                               metrics2.GetCounter("machine_store_update"),
		updateDataToErrorCounter:                    metrics2.GetCounter("machine_store_update_datato_error"),
		watchReceiveSnapshotCounter:                 metrics2.GetCounter("machine_store_watch_receive_snapshot"),
		watchDataToErrorCounter:                     metrics2.GetCounter("machine_store_watch_datato_error"),
		listCounter:                                 metrics2.GetCounter("machine_store_list"),
		deleteCounter:                               metrics2.GetCounter("machine_store_delete"),
		listIterFailureCounter:                      metrics2.GetCounter("machine_store_list_iter_error"),
		watchForDeletablePodsReceiveSnapshotCounter: metrics2.GetCounter("machine_store_watch_for_deletable_pods_receive_snapshot"),
		watchForDeletablePodsDataToErrorCounter:     metrics2.GetCounter("machine_store_watch_for_deletable_pods_datato_error"),
		watchForPowerCycleReceiveSnapshotCounter:    metrics2.GetCounter("machine_store_watch_for_power_cycle_receive_snapshot"),
		watchForPowerCycleDataToErrorCounter:        metrics2.GetCounter("machine_store_watch_for_power_cycle_datato_error"),
	}, nil
}

// Update implements the Store interface.
func (st *StoreImpl) Update(ctx context.Context, machineID string, txCallback TxCallback) error {
	st.updateCounter.Inc(1)
	docRef := st.machinesCollection.Doc(machineID)
	return st.firestoreClient.RunTransaction(ctx, "store", "update", updateRetries, updateTimeout, func(ctx context.Context, tx *gcfirestore.Transaction) error {
		var storeDescription storeDescription
		machineDescription := machine.NewDescription()
		machineDescription.Dimensions[machine.DimID] = []string{machineID}
		if snap, err := tx.Get(docRef); err == nil {
			if err := snap.DataTo(&storeDescription); err != nil {
				st.updateDataToErrorCounter.Inc(1)
				return skerr.Wrapf(err, "Failed to deserialize firestore Get response for %q", machineID)
			}
			machineDescription = storeToMachineDescription(storeDescription)
		} else if st, ok := status.FromError(err); ok && st.Code() != codes.NotFound {
			return skerr.Wrapf(err, "Failed querying firestore for %q", machineID)
		}

		updatedMachineDescription := txCallback(machineDescription)
		updatedStoreDescription := machineDescriptionToStoreDescription(updatedMachineDescription)

		return tx.Set(docRef, &updatedStoreDescription)
	})
}

// Watch implements the Store interface.
func (st *StoreImpl) Watch(ctx context.Context, machineID string) <-chan machine.Description {
	iter := st.machinesCollection.Doc(machineID).Snapshots(ctx)
	ch := make(chan machine.Description)
	go func() {
		for {
			snap, err := iter.Next()
			if err != nil {
				if ctx.Err() == context.Canceled {
					sklog.Warningf("Context canceled; closing channel: %s", err)
				} else if st, ok := status.FromError(err); ok && st.Code() == codes.Canceled {
					sklog.Warningf("Context canceled; closing channel: %s", err)
				} else {
					sklog.Errorf("iter returned error; closing channel: %s", err)
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
			machineDescription := storeToMachineDescription(storeDescription)
			st.watchReceiveSnapshotCounter.Inc(1)
			ch <- machineDescription
		}
	}()
	return ch
}

// WatchForDeletablePods implements the Store interface.
func (st *StoreImpl) WatchForDeletablePods(ctx context.Context) <-chan string {
	q := st.machinesCollection.Where("ScheduledForDeletion", ">", "").Where("RunningSwarmingTask", "==", false)
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
					st.watchForDeletablePodsDataToErrorCounter.Inc(1)
					continue
				}
				machineDescription := storeToMachineDescription(storeDescription)
				st.watchForDeletablePodsReceiveSnapshotCounter.Inc(1)
				ch <- machineDescription.PodName
			}
		}
	}()
	return ch
}

// WatchForPowerCycle implements the Store interface.
func (st *StoreImpl) WatchForPowerCycle(ctx context.Context) <-chan string {
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
				machineDescription := storeToMachineDescription(storeDescription)
				st.watchForPowerCycleReceiveSnapshotCounter.Inc(1)
				machineID := machineDescription.Dimensions[machine.DimID][0]
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

// List implements the Store interface.
func (st *StoreImpl) List(ctx context.Context) ([]machine.Description, error) {
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
		machineDescription := storeToMachineDescription(storeDescription)
		ret = append(ret, machineDescription)
	}
	return ret, nil
}

// Delete implements the Store interface.
func (st *StoreImpl) Delete(ctx context.Context, machineID string) error {
	st.deleteCounter.Inc(1)

	_, err := st.machinesCollection.Doc(machineID).Delete(ctx)
	return err
}

func machineDescriptionToStoreDescription(m machine.Description) storeDescription {
	return storeDescription{
		OS:                   m.Dimensions[machine.DimOS],
		DeviceType:           m.Dimensions[machine.DimDeviceType],
		Quarantined:          m.Dimensions[machine.DimQuarantined],
		Mode:                 m.Mode,
		LastUpdated:          m.LastUpdated,
		ScheduledForDeletion: m.ScheduledForDeletion,
		RunningSwarmingTask:  m.RunningSwarmingTask,
		PowerCycle:           m.PowerCycle,
		MachineDescription:   m,
	}
}

func storeToMachineDescription(s storeDescription) machine.Description {
	return s.MachineDescription
}

// Affirm that StoreImpl implements the Store interface.
var _ Store = (*StoreImpl)(nil)
