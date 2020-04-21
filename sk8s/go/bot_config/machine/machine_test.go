// Package machine is for interacting with the machine state server. See //machine.
package machine

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/executil"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/machine/go/machine"
	"go.skia.org/infra/machine/go/machine/source/pubsubsource"
	"go.skia.org/infra/machine/go/machineserver/config"
	"go.skia.org/infra/sk8s/go/bot_config/swarming"
	"google.golang.org/api/option"
)

const (
	adbShellGetPropSuccess = `[ro.product.manufacturer]: [asus]`
	adbShellDumpSysBattery = `This is dumpsys output.`
)

func setupConfig(t *testing.T) (context.Context, *pubsub.Topic, config.InstanceConfig) {
	require.NotEmpty(t, os.Getenv("FIRESTORE_EMULATOR_HOST"), "This test requires the firestore emulator.")
	unittest.RequiresPubSubEmulator(t)

	ctx := context.Background()
	rand.Seed(time.Now().Unix())
	instanceConfig := config.InstanceConfig{
		Source: config.Source{
			Project: "test-project",
			Topic:   fmt.Sprintf("events-%d", rand.Int63()),
		},
		Store: config.Store{
			Project:  "test-project",
			Instance: fmt.Sprintf("test-%d", rand.Int63()),
		},
	}

	ts, err := auth.NewDefaultTokenSource(true, pubsub.ScopePubSub)
	require.NoError(t, err)
	pubsubClient, err := pubsub.NewClient(ctx, instanceConfig.Source.Project, option.WithTokenSource(ts))
	require.NoError(t, err)

	// Create the topic.
	topic := pubsubClient.Topic(instanceConfig.Source.Topic)
	ok, err := topic.Exists(ctx)
	require.NoError(t, err)
	if !ok {
		topic, err = pubsubClient.CreateTopic(ctx, instanceConfig.Source.Topic)
	}
	topic.Stop()
	assert.NoError(t, err)

	return ctx, topic, instanceConfig
}

func TestStart_InterrogatesDeviceInitiallyAndOnTimer(t *testing.T) {
	// Manual because we are testing pubsub.
	unittest.ManualTest(t)
	ctx, _, instanceConfig := setupConfig(t)
	ctx, cancel := context.WithCancel(ctx)

	// Use source to read pubsub events.
	source, err := pubsubsource.New(ctx, true, instanceConfig)
	require.NoError(t, err)

	// Set the SWARMING_BOT_ID env variable.
	oldVar := os.Getenv(swarming.SwarmingBotIDEnvVar)
	err = os.Setenv(swarming.SwarmingBotIDEnvVar, "my-test-bot-001")
	require.NoError(t, err)
	defer func() {
		err = os.Setenv(swarming.SwarmingBotIDEnvVar, oldVar)
		require.NoError(t, err)
	}()

	// Create a Machine instance.
	m, err := New(ctx, true, instanceConfig)
	require.NoError(t, err)
	assert.Equal(t, "my-test-bot-001", m.MachineID)

	// Write a description into firestore. We expect the dimensions here to
	// bubble down to the machine
	err = m.store.Update(ctx, "my-test-bot-001", func(machine.Description) machine.Description {
		ret := machine.NewDescription()
		ret.Mode = machine.ModeMaintenance
		ret.Dimensions["foo"] = []string{"bar"}
		return ret
	})
	require.NoError(t, err)

	// Set up fakes for adb. We have two sets of 3 since Start calls
	// interrogateAndSend, and then util.RepeatCtx, which also calls
	// interrogateAndSend.
	ctx = executil.WithFakeTests(ctx,
		"Test_FakeExe_AdbShellGetProp_Success",
		"Test_FakeExe_RawDumpSys_Success",
		"Test_FakeExe_RawDumpSys_Success",
		"Test_FakeExe_AdbShellGetProp_Success",
		"Test_FakeExe_RawDumpSys_Success",
		"Test_FakeExe_RawDumpSys_Success",
	)

	// Call Start().
	err = m.Start(ctx)
	require.NoError(t, err)

	// Start() emits a pubsub event before it returns, so check we received the
	// expected machine.Event.
	ch, err := source.Start(ctx)
	require.NoError(t, err)
	event := <-ch

	assert.Equal(t,
		machine.Event{
			EventType: "raw_state",
			Android: machine.Android{
				GetProp:               adbShellGetPropSuccess,
				DumpsysBattery:        adbShellDumpSysBattery,
				DumpsysThermalService: adbShellDumpSysBattery,
			},
			Host: machine.Host{
				Name: "my-test-bot-001",
				Rack: "",
			},
		},
		event)

	// Let the machine.Event get sent via pubsub. OK since this is a manual test.
	time.Sleep(time.Second)

	// Cancel both Go routines inside Start().
	cancel()

	// Confirm the context is cancelled by waiting for the channel to close.
	for range ch {
	}

	assert.Equal(t, int64(1), m.storeWatchArrivalCounter.Get())
	assert.Equal(t, int64(0), m.interrogateAndSendFailures.Get())

	// Confirm the firestore write make it all the way to Dims().
	assert.Equal(t, machine.SwarmingDimensions{"foo": {"bar"}}, m.Dims())
}

func Test_FakeExe_AdbShellGetProp_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "shell", "getprop"}, args)

	fmt.Print(adbShellGetPropSuccess)
	os.Exit(0)
}

func Test_FakeExe_RawDumpSys_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	fmt.Print(adbShellDumpSysBattery)
	os.Exit(0)
}

func TestStart_AdbFailsToTalkToDevice_EmptyEventsSentToServer(t *testing.T) {
	// Manual because we are testing pubsub.
	unittest.ManualTest(t)
	ctx, _, instanceConfig := setupConfig(t)
	ctx, cancel := context.WithCancel(ctx)

	// Use source to read pubsub events.
	source, err := pubsubsource.New(ctx, true, instanceConfig)
	require.NoError(t, err)

	// Set the SWARMING_BOT_ID env variable.
	oldVar := os.Getenv(swarming.SwarmingBotIDEnvVar)
	err = os.Setenv(swarming.SwarmingBotIDEnvVar, "my-test-bot-001")
	require.NoError(t, err)
	defer func() {
		err = os.Setenv(swarming.SwarmingBotIDEnvVar, oldVar)
		assert.NoError(t, err)
	}()

	// Create a Machine instance.
	m, err := New(ctx, true, instanceConfig)
	require.NoError(t, err)

	// Set up fakes for adb. We have two sets of 3 since Start calls
	// interrogateAndSend, and then util.RepeatCtx, which also calls
	// interrogateAndSend.
	ctx = executil.WithFakeTests(ctx,
		"Test_FakeExe_AdbFail",
		"Test_FakeExe_AdbFail",
		"Test_FakeExe_AdbFail",
		"Test_FakeExe_AdbFail",
		"Test_FakeExe_AdbFail",
		"Test_FakeExe_AdbFail",
	)

	// Call Start().
	err = m.Start(ctx)
	require.NoError(t, err)

	// Start() emits a pubsub event before it returns, so check we received the
	// expected machine.Event.
	ch, err := source.Start(ctx)
	require.NoError(t, err)
	event := <-ch

	assert.Equal(t,
		machine.Event{
			EventType: "raw_state",
			Android: machine.Android{
				GetProp:               "",
				DumpsysBattery:        "",
				DumpsysThermalService: "",
			},
			Host: machine.Host{
				Name: "my-test-bot-001",
				Rack: "",
			},
		},
		event)

	// Let the machine.Event get sent via pubsub. OK since this is a manual test.
	time.Sleep(time.Second)

	// Cancel both Go routines inside Start().
	cancel()

	// Confirm the context is cancelled by waiting for the channel to close.
	for range ch {
	}

	assert.Equal(t, int64(0), m.interrogateAndSendFailures.Get())
}

func Test_FakeExe_AdbFail(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	os.Exit(1)
}
