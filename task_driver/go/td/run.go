package td

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"golang.org/x/oauth2"
	compute "google.golang.org/api/compute/v1"
)

const (
	// PubSub topic name for task driver metadata.
	PUBSUB_TOPIC = "task-driver"
	// PubSub topic name for task driver logs.
	PUBSUB_TOPIC_LOGS = "task-driver-logs"

	// Log ID for all Task Drivers. Logs are labeled with task ID and step
	// ID as well, and those labels should be used for filtering in most
	// cases.
	LOG_ID = "task-driver"

	// Special ID of the root step.
	STEP_ID_ROOT = "root"

	// Environment variables provided to all Swarming tasks.
	ENVVAR_SWARMING_BOT    = "SWARMING_BOT_ID"
	ENVVAR_SWARMING_SERVER = "SWARMING_SERVER"
	ENVVAR_SWARMING_TASK   = "SWARMING_TASK_ID"
)

var (
	// Auth scopes required for all task_drivers.
	SCOPES = []string{compute.CloudPlatformScope}
)

// RunProperties are properties for a single run of a Task Driver.
type RunProperties struct {
	Local          bool   `json:"local"`
	SwarmingBot    string `json:"swarmingBot,omitempty"`
	SwarmingServer string `json:"swarmingServer,omitempty"`
	SwarmingTask   string `json:"swarmingTask,omitempty"`
}

// Return an error if the RunProperties are not valid.
func (p *RunProperties) Validate() error {
	if p.Local {
		if p.SwarmingBot != "" {
			return errors.New("SwarmingBot must be empty for local runs!")
		}
		if p.SwarmingServer != "" {
			return errors.New("SwarmingServer must be empty for local runs!")
		}
		if p.SwarmingTask != "" {
			return errors.New("SwarmingTask must be empty for local runs!")
		}
	} else {
		if p.SwarmingBot == "" {
			return errors.New("SwarmingBot is required for non-local runs!")
		}
		if p.SwarmingServer == "" {
			return errors.New("SwarmingServer is required for non-local runs!")
		}
		if p.SwarmingTask == "" {
			return errors.New("SwarmingTask is required for non-local runs!")
		}
	}
	return nil
}

// Return a copy of the RunProperties.
func (p *RunProperties) Copy() *RunProperties {
	if p == nil {
		return nil
	}
	return &RunProperties{
		Local:          p.Local,
		SwarmingBot:    p.SwarmingBot,
		SwarmingServer: p.SwarmingServer,
		SwarmingTask:   p.SwarmingTask,
	}
}

// StartRunWithErr begins a new test automation run, returning any error which
// occurs.
func StartRunWithErr(projectId, taskId, taskName, output *string, local *bool) (context.Context, error) {
	common.Init()

	// TODO(borenet): Catch SIGINT, SIGKILL and report.

	// Gather RunProperties.
	swarmingBot := os.Getenv(ENVVAR_SWARMING_BOT)
	swarmingServer := os.Getenv(ENVVAR_SWARMING_SERVER)
	swarmingTask := os.Getenv(ENVVAR_SWARMING_TASK)

	// "reproduce" is supplied by "swarming.py reproduce" and indicates that
	// this is actually a local run, but --local won't have been provided
	// because the command was copied directly from the Swarming task.
	if swarmingTask == "reproduce" || swarmingBot == "reproduce" {
		*local = true
		swarmingBot = ""
		swarmingServer = ""
		swarmingTask = ""
	}
	if *local {
		// Check to make sure we're not actually running in production.
		// Note that the presence of SWARMING_SERVER does not indicate
		// that we're running in production, because it can be used with
		// swarming.py as an alternative to --swarming.
		errTmpl := "--local was supplied but %s environment variable was found. Was --local used by accident?"
		if swarmingBot != "" {
			return nil, fmt.Errorf(errTmpl, ENVVAR_SWARMING_BOT)
		} else if swarmingTask != "" {
			return nil, fmt.Errorf(errTmpl, ENVVAR_SWARMING_TASK)
		}

		// Prevent clobbering real task data for local tasks.
		hostname, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		*taskId = fmt.Sprintf("%s_%s", hostname, uuid.New())
	} else {
		// Check to make sure that we're not running locally and the
		// user forgot to use --local.
		errTmpl := "--local was not supplied but environment variable %s was not found. Did you forget to use --local?"
		if swarmingBot == "" {
			return nil, fmt.Errorf(errTmpl, ENVVAR_SWARMING_BOT)
		} else if swarmingServer == "" {
			return nil, fmt.Errorf(errTmpl, ENVVAR_SWARMING_SERVER)
		} else if swarmingTask == "" {
			return nil, fmt.Errorf(errTmpl, ENVVAR_SWARMING_TASK)
		}
	}

	// Validate properties and flags.
	props := &RunProperties{
		Local:          *local,
		SwarmingBot:    swarmingBot,
		SwarmingServer: swarmingServer,
		SwarmingTask:   swarmingTask,
	}
	if err := props.Validate(); err != nil {
		return nil, err
	}
	if *projectId == "" {
		return nil, fmt.Errorf("Project ID is required.")
	}
	if *taskId == "" {
		return nil, fmt.Errorf("Task ID is required.")
	}
	if *taskName == "" {
		return nil, fmt.Errorf("Task name is required.")
	}

	// Create the token source.
	var ts oauth2.TokenSource
	if *local {
		var err error
		ts, err = auth.NewDefaultTokenSource(*local, SCOPES...)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		ts, err = auth.NewLUCIContextTokenSource(SCOPES...)
		if err != nil {
			return nil, fmt.Errorf("Failed to obtain LUCI TokenSource: %s", err)
		}
	}

	// Initialize Cloud Logging.
	labels := map[string]string{
		"taskId":   *taskId,
		"taskName": *taskName,
	}
	ctx := context.Background()
	logger, err := sklog.NewCloudLogger(ctx, *projectId, LOG_ID, ts, labels)
	if err != nil {
		return nil, err
	}
	sklog.SetLogger(logger)

	// Dump environment variables.
	sklog.Infof("Environment:\n%s", strings.Join(os.Environ(), "\n"))

	// Connect receivers.
	cloudLogging, err := NewCloudLoggingReceiver(logger.Logger())
	if err != nil {
		return nil, err
	}
	report := newReportReceiver(*output)
	receiver := MultiReceiver([]Receiver{
		cloudLogging,
		&DebugReceiver{},
		report,
	})

	// Set up and return the root-level Step.
	ctx = newRun(ctx, receiver, *taskId, *taskName, props)
	return ctx, nil
}

// StartRun begins a new test automation run, panicking if any setup fails.
func StartRun(projectId, taskId, taskName, output *string, local *bool) context.Context {
	ctx, err := StartRunWithErr(projectId, taskId, taskName, output, local)
	if err != nil {
		sklog.Fatalf("Failed task_driver.StartRun(): %s", err)
	}
	return ctx
}

// Perform any cleanup work for the run. Should be deferred in main().
func EndRun(ctx context.Context) {
	defer util.Close(getRun(ctx))

	// Mark the root step as finished.
	finishStep(ctx, recover())
}

// run represents a full test automation run.
type run struct {
	receiver Receiver
	taskId   string
	msgIndex int32
}

// newRun returns a context.Context representing a Task Driver run, including
// creation of a root step.
func newRun(ctx context.Context, rec Receiver, taskId, taskName string, props *RunProperties) context.Context {
	r := &run{
		receiver: rec,
		taskId:   taskId,
	}
	ctx = setRun(ctx, r)
	r.send(&Message{
		Type: MSG_TYPE_RUN_STARTED,
		Run:  props,
	})
	ctx = newStep(ctx, STEP_ID_ROOT, nil, Props(taskName))
	return ctx
}

// Send the given message to the receiver. Does not return an error, even if
// sending fails.
func (r *run) send(msg *Message) {
	msg.Index = int(atomic.AddInt32(&r.msgIndex, 1))
	msg.TaskId = r.taskId
	msg.Timestamp = time.Now().UTC()
	if err := msg.Validate(); err != nil {
		sklog.Error(err)
	}
	if err := r.receiver.HandleMessage(msg); err != nil {
		// Just log the error but don't return it.
		// TODO(borenet): How do we handle this?
		sklog.Error(err)
	}
}

// Send a Message indicating that a new step has started.
func (r *run) Start(props *StepProperties) {
	msg := &Message{
		Type:   MSG_TYPE_STEP_STARTED,
		StepId: props.Id,
		Step:   props,
	}
	r.send(msg)
}

// Send a Message with additional data for the current step.
func (r *run) AddStepData(id string, typ DataType, d interface{}) {
	msg := &Message{
		Type:     MSG_TYPE_STEP_DATA,
		StepId:   id,
		Data:     d,
		DataType: typ,
	}
	r.send(msg)
}

// Send a Message indicating that the current step has failed with the given
// error.
func (r *run) Failed(id string, err error) {
	msg := &Message{
		StepId: id,
		Error:  err.Error(),
	}
	if IsInfraError(err) {
		msg.Type = MSG_TYPE_STEP_EXCEPTION
	} else {
		msg.Type = MSG_TYPE_STEP_FAILED
	}
	r.send(msg)
}

// Send a Message indicating that the current step has finished.
func (r *run) Finish(id string) {
	msg := &Message{
		Type:   MSG_TYPE_STEP_FINISHED,
		StepId: id,
	}
	r.send(msg)
}

// Open a log stream.
func (r *run) LogStream(stepId, logName, severity string) io.Writer {
	logId := uuid.New().String() // TODO(borenet): Come up with a better ID.
	rv, err := r.receiver.LogStream(stepId, logId, severity)
	if err != nil {
		panic(err)
	}

	// Emit step data for the log stream.
	r.AddStepData(stepId, DATA_TYPE_LOG, &LogData{
		Name:     logName,
		Id:       logId,
		Severity: severity,
	})
	return rv
}

// Close the run.
func (r *run) Close() error {
	return r.receiver.Close()
}
