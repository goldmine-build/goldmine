package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/now"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/machine/go/machine"
	"go.skia.org/infra/machine/go/machineserver/config"
)

func TestConvertDescription_NoDimensions(t *testing.T) {
	unittest.SmallTest(t)
	d := machine.NewDescription(now.TimeTravelingContext(fakeTime))
	m := convertDescription(d)
	assert.Equal(t, storeDescription{
		Mode:           machine.ModeAvailable,
		AttachedDevice: machine.AttachedDeviceNone,
		LastUpdated:    fakeTime,
		Dimensions:     machine.SwarmingDimensions{},
	}, m)
}

func TestConvertDescription_WithDimensions(t *testing.T) {
	unittest.SmallTest(t)
	d := machine.NewDescription(now.TimeTravelingContext(fakeTime))
	d.AttachedDevice = machine.AttachedDeviceAdb
	d.Dimensions = machine.SwarmingDimensions{
		machine.DimOS:          []string{"Android"},
		machine.DimDeviceType:  []string{"sailfish"},
		machine.DimQuarantined: []string{"Device sailfish too hot."},
	}
	expectedDims := d.Dimensions.Copy()

	m := convertDescription(d)
	assert.Equal(t, storeDescription{
		AttachedDevice: machine.AttachedDeviceAdb,
		OS:             []string{"Android"},
		DeviceType:     []string{"sailfish"},
		Quarantined:    []string{"Device sailfish too hot."},
		Dimensions:     expectedDims,
		Mode:           machine.ModeAvailable,
		LastUpdated:    fakeTime,
	}, m)
}

func TestConvertDescription_WithPowerCycle(t *testing.T) {
	unittest.SmallTest(t)
	d := machine.NewDescription(now.TimeTravelingContext(fakeTime))
	d.AttachedDevice = machine.AttachedDeviceAdb
	d.Dimensions = machine.SwarmingDimensions{
		machine.DimOS: []string{"Android"},
	}
	d.PowerCycle = true

	expectedDims := d.Dimensions.Copy()

	m := convertDescription(d)
	assert.Equal(t, storeDescription{
		AttachedDevice: machine.AttachedDeviceAdb,
		OS:             []string{"Android"},
		Mode:           machine.ModeAvailable,
		Dimensions:     expectedDims,
		LastUpdated:    fakeTime,
		PowerCycle:     true,
	}, m)
}

func setupForTest(t *testing.T) (context.Context, config.InstanceConfig) {
	unittest.RequiresFirestoreEmulator(t)
	cfg := config.InstanceConfig{
		Store: config.Store{
			Project:  "test-project",
			Instance: fmt.Sprintf("test-%s", uuid.New()),
		},
	}
	return now.TimeTravelingContext(fakeTime), cfg
}

func setupForFlakyTest(t *testing.T) (context.Context, config.InstanceConfig) {
	unittest.RequiresFirestoreEmulatorWithTestCaseSpecificInstanceUnderRBE(t)
	cfg := config.InstanceConfig{
		Store: config.Store{
			Project:  "test-project",
			Instance: fmt.Sprintf("test-%s", uuid.New()),
		},
	}
	return now.TimeTravelingContext(fakeTime), cfg
}

func TestNew(t *testing.T) {
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	_, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)
}

func TestUpdate_CanUpdateEvenIfDescriptionDoesntExist(t *testing.T) {
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)

	store.updateCounter.Reset()
	called := false
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-001", func(previous machine.Description) machine.Description {
		assert.Equal(t, machine.ModeAvailable, previous.Mode)
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		called = true
		return ret
	})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, int64(1), store.updateCounter.Get())
	assert.Equal(t, int64(0), store.updateDataToErrorCounter.Get())

	snap, err := store.machinesCollection.Doc("skia-rpi2-rack2-shelf1-001").Get(ctx)
	require.NoError(t, err)
	var storedDescription machine.Description
	err = snap.DataTo(&storedDescription)
	require.NoError(t, err)
	assert.Equal(t, machine.ModeMaintenance, storedDescription.Mode)
	assert.NoError(t, store.firestoreClient.Close())
}

func TestUpdate_CanUpdateIfDescriptionExists(t *testing.T) {
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)

	// First write a Description.
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-001", func(previous machine.Description) machine.Description {
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		ret.Dimensions[machine.DimOS] = []string{"Android"}
		return ret
	})
	require.NoError(t, err)

	// Now confirm we get the Description we previously wrote on the next update.
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-001", func(previous machine.Description) machine.Description {
		assert.Equal(t, machine.ModeMaintenance, previous.Mode)
		assert.Equal(t, []string{"Android"}, previous.Dimensions["os"])
		assert.Empty(t, previous.Dimensions[machine.DimDeviceType])
		ret := previous.Copy()
		ret.Mode = machine.ModeAvailable
		return ret
	})
	require.NoError(t, err)
	assert.NoError(t, store.firestoreClient.Close())
}

func TestWatch_StartWatchAfterMachineExists(t *testing.T) {
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// First create the document.
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-001", func(previous machine.Description) machine.Description {
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		ret.Dimensions[machine.DimOS] = []string{"Android"}
		ret.Annotation.Message = "Hello World!"
		return ret
	})
	require.NoError(t, err)

	// Then add the watch.
	ch := store.Watch(ctx, "skia-rpi2-rack2-shelf1-001")

	// Wait for first description.
	m := <-ch
	assert.Equal(t, machine.ModeMaintenance, m.Mode)
	assert.Equal(t, machine.SwarmingDimensions{
		machine.DimID: {"skia-rpi2-rack2-shelf1-001"},
		machine.DimOS: {"Android"},
	}, m.Dimensions)
	assert.Equal(t, "Hello World!", m.Annotation.Message)
	assert.NoError(t, store.firestoreClient.Close())
}

func TestWatch_StartWatchBeforeMachineExists_ContinuesToTryUntilMachineExists(t *testing.T) {
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)

	// Set this to 0 so we don't have any waiting during tests.
	watchRecoverBackoff = 0

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// First add the watch.
	ch := store.Watch(ctx, "skia-rpi2-rack2-shelf1-001")

	// Then create the document.
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-001", func(previous machine.Description) machine.Description {
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		ret.Dimensions[machine.DimOS] = []string{"Android"}
		ret.Annotation.Message = "Hello World!"
		return ret
	})
	require.NoError(t, err)

	// Wait for first description.
	m := <-ch
	assert.Equal(t, machine.ModeMaintenance, m.Mode)
	assert.Equal(t, machine.SwarmingDimensions{
		machine.DimID: {"skia-rpi2-rack2-shelf1-001"},
		machine.DimOS: {"Android"},
	}, m.Dimensions)
	assert.Equal(t, "Hello World!", m.Annotation.Message)
	assert.NoError(t, store.firestoreClient.Close())
}

func TestWatch_IsCancellable(t *testing.T) {
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(ctx)

	// First create the document.
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-001", func(previous machine.Description) machine.Description {
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		return ret
	})
	require.NoError(t, err)

	// Then add the watch.
	ch := store.Watch(ctx, "skia-rpi2-rack2-shelf1-001")

	cancel()

	// The test passes if we get past this loop since that means the channel was closed.
	for range ch {
	}
	assert.NoError(t, store.firestoreClient.Close())
}

func TestList_Success(t *testing.T) {
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)

	// List on an empty collection is OK.
	descriptions, err := store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, descriptions, 0)

	// Add a single description.
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-001", func(previous machine.Description) machine.Description {
		assert.Equal(t, machine.ModeAvailable, previous.Mode)
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		ret.Dimensions["foo"] = []string{"bar", "baz"}
		return ret
	})
	require.NoError(t, err)

	// Confirm it appears in the list.
	descriptions, err = store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, descriptions, 1)
	assert.Equal(t, machine.SwarmingDimensions{
		"foo":         {"bar", "baz"},
		machine.DimID: {"skia-rpi2-rack2-shelf1-001"},
	}, descriptions[0].Dimensions)

	// Add a second description.
	err = store.Update(ctx, "skia-rpi2-rack2-shelf1-002", func(previous machine.Description) machine.Description {
		assert.Equal(t, machine.ModeAvailable, previous.Mode)
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		ret.Dimensions["foo"] = []string{"quux"}
		return ret
	})
	require.NoError(t, err)

	// Confirm they both show up in the list.
	descriptions, err = store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, descriptions, 2)
}

func TestDelete_Success(t *testing.T) {
	const machineName = "skia-rpi2-rack2-shelf1-001"
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)
	store.deleteCounter.Reset()

	err = store.Update(ctx, machineName, func(previous machine.Description) machine.Description {
		ret := previous.Copy()
		ret.Mode = machine.ModeMaintenance
		return ret
	})
	require.NoError(t, err)

	err = store.Delete(ctx, machineName)
	require.NoError(t, err)

	assert.Equal(t, int64(1), store.deleteCounter.Get())

	// Confirm it is really gone.
	_, err = store.machinesCollection.Doc(machineName).Get(ctx)
	require.Error(t, err)
}

func TestDelete_NoErrorIfMachineDoesntExist(t *testing.T) {
	const machineName = "skia-rpi2-rack2-shelf1-001"
	unittest.LargeTest(t)
	ctx, cfg := setupForTest(t)
	store, err := NewFirestoreImpl(ctx, true, cfg)
	require.NoError(t, err)
	store.deleteCounter.Reset()

	err = store.Delete(ctx, machineName)
	require.NoError(t, err)

	assert.Equal(t, int64(1), store.deleteCounter.Get())
}

var fakeTime = time.Date(2021, time.September, 1, 0, 0, 0, 0, time.UTC)

func TestForceToAttachedDevice(t *testing.T) {
	unittest.SmallTest(t)
	assert.Equal(t, machine.AttachedDeviceSSH, forceToAttachedDevice(machine.AttachedDeviceSSH))
	assert.Equal(t, machine.AttachedDeviceNone, forceToAttachedDevice(machine.AttachedDevice("this is not a valid attached device name")))
}
