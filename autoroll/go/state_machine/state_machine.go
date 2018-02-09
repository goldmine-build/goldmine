package state_machine

import (
	"context"
	"fmt"
	"path"
	"time"

	"go.skia.org/infra/autoroll/go/modes"
	"go.skia.org/infra/go/autoroll"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/state_machine"
	"go.skia.org/infra/go/util"
)

/*
	State machine for the autoroller.
*/

const (
	// Throttling parameters.
	DEFAULT_SAFETY_THROTTLE_ATTEMPT_COUNT = 3
	DEFAULT_SAFETY_THROTTLE_TIME_WINDOW   = 30 * time.Minute

	// State names.
	S_NORMAL_IDLE                  = "idle"
	S_NORMAL_ACTIVE                = "active"
	S_NORMAL_SUCCESS               = "success"
	S_NORMAL_SUCCESS_THROTTLED     = "success throttled"
	S_NORMAL_FAILURE               = "failure"
	S_NORMAL_FAILURE_THROTTLED     = "failure throttled"
	S_NORMAL_SAFETY_THROTTLED      = "safety throttled"
	S_DRY_RUN_IDLE                 = "dry run idle"
	S_DRY_RUN_ACTIVE               = "dry run active"
	S_DRY_RUN_SUCCESS              = "dry run success"
	S_DRY_RUN_SUCCESS_LEAVING_OPEN = "dry run success; leaving open"
	S_DRY_RUN_FAILURE              = "dry run failure"
	S_DRY_RUN_FAILURE_THROTTLED    = "dry run failure throttled"
	S_DRY_RUN_SAFETY_THROTTLED     = "dry run safety throttled"
	S_STOPPED                      = "stopped"

	// Transition function names.
	F_NOOP                   = "no-op"
	F_UPDATE_REPOS           = "update repos"
	F_UPLOAD_ROLL            = "upload roll"
	F_UPLOAD_DRY_RUN         = "upload dry run"
	F_UPDATE_ROLL            = "update roll"
	F_SWITCH_TO_DRY_RUN      = "switch roll to dry run"
	F_SWITCH_TO_NORMAL       = "switch roll to normal"
	F_CLOSE_FAILED           = "close roll (failed)"
	F_CLOSE_STOPPED          = "close roll (stopped)"
	F_CLOSE_DRY_RUN_FAILED   = "close roll (dry run failed)"
	F_CLOSE_DRY_RUN_OUTDATED = "close roll (dry run outdated)"
	F_WAIT_FOR_LAND          = "wait for roll to land"
	F_RETRY_FAILED_NORMAL    = "retry failed roll"
	F_RETRY_FAILED_DRY_RUN   = "retry failed dry run"

	// Maximum number of no-op transitions to perform at once. This is an
	// arbitrary limit just to keep us from performing an unbounded number
	// of transitions at a time.
	MAX_NOOP_TRANSITIONS = 10
)

var (
	SAFETY_THROTTLE_CONFIG_DEFAULT = &ThrottleConfig{
		AttemptCount: DEFAULT_SAFETY_THROTTLE_ATTEMPT_COUNT,
		TimeWindow:   DEFAULT_SAFETY_THROTTLE_TIME_WINDOW,
	}
)

// Interface for interacting with a single autoroll CL.
type RollCLImpl interface {
	// Add a comment to the CL.
	AddComment(string) error

	// Close the CL. The first string argument is the result of the roll,
	// and the second is the message to add to the CL on closing.
	Close(context.Context, string, string) error

	// Return true iff the roll has finished (ie. succeeded or failed).
	IsFinished() bool

	// Return true iff the roll succeeded.
	IsSuccess() bool

	// Return true iff the dry run is finished.
	IsDryRunFinished() bool

	// Return true iff the dry run succeeded.
	IsDryRunSuccess() bool

	// Retry the CQ in the case of a failure.
	RetryCQ(context.Context) error

	// Retry a dry run in the case of a failure.
	RetryDryRun(context.Context) error

	// The revision this roll is rolling to.
	RollingTo() string

	// Set the dry run bit on the CL.
	SwitchToDryRun(context.Context) error

	// Set the full CQ bit on the CL.
	SwitchToNormal(context.Context) error

	// Update our local copy of the CL from the codereview server.
	Update(context.Context) error
}

// Interface for interacting with the other elements of an autoroller.
type AutoRollerImpl interface {
	// Upload a new roll. AutoRollerImpl should track the created roll.
	UploadNewRoll(ctx context.Context, from, to string, dryRun bool) error

	// Return the currently-active roll. May be nil if no roll exists.
	GetActiveRoll() RollCLImpl

	// Return the currently-rolled revision of the sub-project.
	GetCurrentRev() string

	// Return the next revision of the sub-project which we want to roll.
	// This is the same as GetCurrentRev when the sub-project is up-to-date.
	GetNextRollRev() string

	// Return the configured maximum roll frequency for the AutoRoller.
	GetMaxRollFrequency() time.Duration

	// Return the current mode of the AutoRoller.
	GetMode() string

	// Return true if we have already rolled past the given revision.
	RolledPast(context.Context, string) (bool, error)

	// Update the project and sub-project repos.
	UpdateRepos(context.Context) error
}

// AutoRollStateMachine is a StateMachine for the AutoRoller.
type AutoRollStateMachine struct {
	a              AutoRollerImpl
	attemptCounter *util.PersistentAutoDecrementCounter
	failCounter    *util.PersistentAutoDecrementCounter
	s              *state_machine.StateMachine
	successCounter *util.PersistentAutoDecrementCounter
	tc             *ThrottleConfig
}

// ThrottleConfig determines the throttling behavior for the roller.
type ThrottleConfig struct {
	AttemptCount int64
	TimeWindow   time.Duration
}

// New returns a StateMachine for the autoroller.
func New(impl AutoRollerImpl, workdir string, tc *ThrottleConfig) (*AutoRollStateMachine, error) {
	// Global state.
	if tc == nil {
		tc = SAFETY_THROTTLE_CONFIG_DEFAULT
	}
	attemptCounter, err := util.NewPersistentAutoDecrementCounter(path.Join(workdir, "attempt_counter"), tc.TimeWindow)
	if err != nil {
		return nil, err
	}
	failCounter, err := util.NewPersistentAutoDecrementCounter(path.Join(workdir, "fail_counter"), time.Hour)
	if err != nil {
		return nil, err
	}
	successCounter, err := util.NewPersistentAutoDecrementCounter(path.Join(workdir, "success_counter"), impl.GetMaxRollFrequency())
	if err != nil {
		return nil, err
	}
	s := &AutoRollStateMachine{
		a:              impl,
		attemptCounter: attemptCounter,
		failCounter:    failCounter,
		s:              nil, // Filled in later.
		successCounter: successCounter,
		tc:             tc,
	}

	b := state_machine.NewBuilder()

	// Named callback functions.
	b.F(F_NOOP, nil)
	b.F(F_UPDATE_REPOS, func(ctx context.Context) error {
		return s.a.UpdateRepos(ctx)
	})
	b.F(F_UPLOAD_ROLL, func(ctx context.Context) error {
		if err := s.attemptCounter.Inc(); err != nil {
			return err
		}
		return s.a.UploadNewRoll(ctx, s.a.GetCurrentRev(), s.a.GetNextRollRev(), false)
	})
	b.F(F_UPLOAD_DRY_RUN, func(ctx context.Context) error {
		if err := s.attemptCounter.Inc(); err != nil {
			return err
		}
		return s.a.UploadNewRoll(ctx, s.a.GetCurrentRev(), s.a.GetNextRollRev(), true)
	})
	b.F(F_UPDATE_ROLL, func(ctx context.Context) error {
		if err := s.a.GetActiveRoll().Update(ctx); err != nil {
			return err
		}
		return s.a.UpdateRepos(ctx)
	})
	b.F(F_CLOSE_FAILED, func(ctx context.Context) error {
		return s.a.GetActiveRoll().Close(ctx, autoroll.ROLL_RESULT_FAILURE, fmt.Sprintf("Commit queue failed; closing this roll."))
	})
	b.F(F_CLOSE_STOPPED, func(ctx context.Context) error {
		return s.a.GetActiveRoll().Close(ctx, autoroll.ROLL_RESULT_FAILURE, fmt.Sprintf("AutoRoller is stopped; closing the active roll."))
	})
	b.F(F_CLOSE_DRY_RUN_FAILED, func(ctx context.Context) error {
		return s.a.GetActiveRoll().Close(ctx, autoroll.ROLL_RESULT_DRY_RUN_FAILURE, fmt.Sprintf("Commit queue failed; closing this roll."))
	})
	b.F(F_CLOSE_DRY_RUN_OUTDATED, func(ctx context.Context) error {
		currentRoll := s.a.GetActiveRoll()
		return currentRoll.Close(ctx, autoroll.ROLL_RESULT_DRY_RUN_SUCCESS, fmt.Sprintf("Repo has passed %s; will open a new dry run.", currentRoll.RollingTo()))
	})
	b.F(F_SWITCH_TO_DRY_RUN, func(ctx context.Context) error {
		return s.a.GetActiveRoll().SwitchToDryRun(ctx)
	})
	b.F(F_SWITCH_TO_NORMAL, func(ctx context.Context) error {
		return s.a.GetActiveRoll().SwitchToNormal(ctx)
	})
	b.F(F_WAIT_FOR_LAND, func(ctx context.Context) error {
		sklog.Infof("Roll succeeded; syncing the repo until it lands.")
		currentRoll := s.a.GetActiveRoll()
		// If the server restarts during the loop below, we'll end up in this state
		// even though there is no active roll.
		if currentRoll == nil {
			sklog.Warningf("GetActiveRoll returned nil in state %q. Continuing transition under the assumption that the roll has landed.", F_WAIT_FOR_LAND)
			return nil
		}
		for {
			sklog.Infof("Syncing, looking for %s...", currentRoll.RollingTo())
			if err := s.a.UpdateRepos(ctx); err != nil {
				return err
			}
			rolledPast, err := s.a.RolledPast(ctx, currentRoll.RollingTo())
			if err != nil {
				return err
			}
			if rolledPast {
				break
			}
			time.Sleep(10 * time.Second)
		}
		return nil
	})
	b.F(F_RETRY_FAILED_NORMAL, func(ctx context.Context) error {
		sklog.Infof("Roll failed but no new commits; retrying CQ.")
		// TODO(borenet): The CQ will fail forever in the case of a
		// merge conflict; we should really patch in the CL, rebase and
		// upload again.
		return s.a.GetActiveRoll().RetryCQ(ctx)
	})
	b.F(F_RETRY_FAILED_DRY_RUN, func(ctx context.Context) error {
		sklog.Infof("Dry run failed but no new commits; retrying CQ.")
		// TODO(borenet): The CQ will fail forever in the case of a
		// merge conflict; we should really patch in the CL, rebase and
		// upload again.
		return s.a.GetActiveRoll().RetryDryRun(ctx)
	})

	// States and transitions.

	// Stopped state.
	b.T(S_STOPPED, S_STOPPED, F_UPDATE_REPOS)
	b.T(S_STOPPED, S_NORMAL_IDLE, F_NOOP)
	b.T(S_STOPPED, S_DRY_RUN_IDLE, F_NOOP)

	// Normal states.
	b.T(S_NORMAL_IDLE, S_STOPPED, F_NOOP)
	b.T(S_NORMAL_IDLE, S_NORMAL_IDLE, F_UPDATE_REPOS)
	b.T(S_NORMAL_IDLE, S_DRY_RUN_IDLE, F_NOOP)
	b.T(S_NORMAL_IDLE, S_NORMAL_SAFETY_THROTTLED, F_NOOP)
	b.T(S_NORMAL_IDLE, S_NORMAL_SUCCESS_THROTTLED, F_NOOP)
	b.T(S_NORMAL_IDLE, S_NORMAL_ACTIVE, F_UPLOAD_ROLL)
	b.T(S_NORMAL_ACTIVE, S_NORMAL_ACTIVE, F_UPDATE_ROLL)
	b.T(S_NORMAL_ACTIVE, S_DRY_RUN_ACTIVE, F_SWITCH_TO_DRY_RUN)
	b.T(S_NORMAL_ACTIVE, S_NORMAL_SUCCESS, F_NOOP)
	b.T(S_NORMAL_ACTIVE, S_NORMAL_FAILURE, F_NOOP)
	b.T(S_NORMAL_ACTIVE, S_STOPPED, F_CLOSE_STOPPED)
	b.T(S_NORMAL_SUCCESS, S_NORMAL_IDLE, F_WAIT_FOR_LAND)
	b.T(S_NORMAL_SUCCESS, S_NORMAL_SUCCESS_THROTTLED, F_WAIT_FOR_LAND)
	b.T(S_NORMAL_SUCCESS_THROTTLED, S_NORMAL_SUCCESS_THROTTLED, F_UPDATE_REPOS)
	b.T(S_NORMAL_SUCCESS_THROTTLED, S_NORMAL_IDLE, F_NOOP)
	b.T(S_NORMAL_SUCCESS_THROTTLED, S_DRY_RUN_IDLE, F_NOOP)
	b.T(S_NORMAL_SUCCESS_THROTTLED, S_STOPPED, F_NOOP)
	b.T(S_NORMAL_FAILURE, S_NORMAL_IDLE, F_CLOSE_FAILED)
	b.T(S_NORMAL_FAILURE, S_NORMAL_FAILURE_THROTTLED, F_NOOP)
	b.T(S_NORMAL_FAILURE_THROTTLED, S_NORMAL_FAILURE_THROTTLED, F_UPDATE_REPOS)
	b.T(S_NORMAL_FAILURE_THROTTLED, S_NORMAL_ACTIVE, F_RETRY_FAILED_NORMAL)
	b.T(S_NORMAL_FAILURE_THROTTLED, S_DRY_RUN_ACTIVE, F_SWITCH_TO_DRY_RUN)
	b.T(S_NORMAL_FAILURE_THROTTLED, S_NORMAL_IDLE, F_CLOSE_FAILED)
	b.T(S_NORMAL_FAILURE_THROTTLED, S_STOPPED, F_CLOSE_STOPPED)
	b.T(S_NORMAL_SAFETY_THROTTLED, S_NORMAL_IDLE, F_NOOP)
	b.T(S_NORMAL_SAFETY_THROTTLED, S_NORMAL_SAFETY_THROTTLED, F_UPDATE_REPOS)

	// Dry run states.
	b.T(S_DRY_RUN_IDLE, S_STOPPED, F_NOOP)
	b.T(S_DRY_RUN_IDLE, S_DRY_RUN_IDLE, F_UPDATE_REPOS)
	b.T(S_DRY_RUN_IDLE, S_NORMAL_IDLE, F_NOOP)
	b.T(S_DRY_RUN_IDLE, S_NORMAL_SUCCESS_THROTTLED, F_NOOP)
	b.T(S_DRY_RUN_IDLE, S_DRY_RUN_SAFETY_THROTTLED, F_NOOP)
	b.T(S_DRY_RUN_IDLE, S_DRY_RUN_ACTIVE, F_UPLOAD_DRY_RUN)
	b.T(S_DRY_RUN_ACTIVE, S_DRY_RUN_ACTIVE, F_UPDATE_ROLL)
	b.T(S_DRY_RUN_ACTIVE, S_NORMAL_ACTIVE, F_SWITCH_TO_NORMAL)
	b.T(S_DRY_RUN_ACTIVE, S_DRY_RUN_SUCCESS, F_NOOP)
	b.T(S_DRY_RUN_ACTIVE, S_DRY_RUN_FAILURE, F_NOOP)
	b.T(S_DRY_RUN_ACTIVE, S_STOPPED, F_CLOSE_STOPPED)
	b.T(S_DRY_RUN_SUCCESS, S_DRY_RUN_IDLE, F_CLOSE_DRY_RUN_OUTDATED)
	b.T(S_DRY_RUN_SUCCESS, S_DRY_RUN_SUCCESS_LEAVING_OPEN, F_NOOP)
	b.T(S_DRY_RUN_SUCCESS_LEAVING_OPEN, S_DRY_RUN_SUCCESS_LEAVING_OPEN, F_UPDATE_REPOS)
	b.T(S_DRY_RUN_SUCCESS_LEAVING_OPEN, S_NORMAL_ACTIVE, F_SWITCH_TO_NORMAL)
	b.T(S_DRY_RUN_SUCCESS_LEAVING_OPEN, S_STOPPED, F_CLOSE_STOPPED)
	b.T(S_DRY_RUN_SUCCESS_LEAVING_OPEN, S_DRY_RUN_IDLE, F_CLOSE_DRY_RUN_OUTDATED)
	b.T(S_DRY_RUN_FAILURE, S_DRY_RUN_IDLE, F_CLOSE_DRY_RUN_FAILED)
	b.T(S_DRY_RUN_FAILURE, S_DRY_RUN_FAILURE_THROTTLED, F_NOOP)
	b.T(S_DRY_RUN_FAILURE_THROTTLED, S_DRY_RUN_FAILURE_THROTTLED, F_UPDATE_REPOS)
	b.T(S_DRY_RUN_FAILURE_THROTTLED, S_DRY_RUN_ACTIVE, F_RETRY_FAILED_DRY_RUN)
	b.T(S_DRY_RUN_FAILURE_THROTTLED, S_DRY_RUN_IDLE, F_CLOSE_DRY_RUN_FAILED)
	b.T(S_DRY_RUN_FAILURE_THROTTLED, S_NORMAL_ACTIVE, F_SWITCH_TO_NORMAL)
	b.T(S_DRY_RUN_FAILURE_THROTTLED, S_STOPPED, F_CLOSE_STOPPED)
	b.T(S_DRY_RUN_SAFETY_THROTTLED, S_DRY_RUN_IDLE, F_NOOP)
	b.T(S_DRY_RUN_SAFETY_THROTTLED, S_DRY_RUN_SAFETY_THROTTLED, F_UPDATE_REPOS)

	// Build the state machine.
	b.SetInitial(S_NORMAL_IDLE)
	sm, err := b.Build(workdir)
	if err != nil {
		return nil, err
	}
	s.s = sm
	return s, nil
}

// Get the next state.
func (s *AutoRollStateMachine) GetNext() (string, error) {
	desiredMode := s.a.GetMode()
	switch state := s.s.Current(); state {
	case S_STOPPED:
		switch desiredMode {
		case modes.MODE_RUNNING:
			return S_NORMAL_IDLE, nil
		case modes.MODE_DRY_RUN:
			return S_DRY_RUN_IDLE, nil
		case modes.MODE_STOPPED:
			return S_STOPPED, nil
		default:
			return "", fmt.Errorf("Invalid mode: %q", desiredMode)
		}
	case S_NORMAL_IDLE:
		switch desiredMode {
		case modes.MODE_RUNNING:
			break
		case modes.MODE_DRY_RUN:
			return S_DRY_RUN_IDLE, nil
		case modes.MODE_STOPPED:
			return S_STOPPED, nil
		default:
			return "", fmt.Errorf("Invalid mode: %q", desiredMode)
		}
		current := s.a.GetCurrentRev()
		next := s.a.GetNextRollRev()
		if current == next {
			return S_NORMAL_IDLE, nil
		} else if s.attemptCounter.Get() >= s.tc.AttemptCount {
			return S_NORMAL_SAFETY_THROTTLED, nil
		} else if s.successCounter.Get() > 0 && s.a.GetMaxRollFrequency() > time.Duration(0) {
			return S_NORMAL_SUCCESS_THROTTLED, nil
		} else {
			return S_NORMAL_ACTIVE, nil
		}
	case S_NORMAL_ACTIVE:
		currentRoll := s.a.GetActiveRoll()
		if currentRoll.IsFinished() {
			if currentRoll.IsSuccess() {
				return S_NORMAL_SUCCESS, nil
			} else {
				return S_NORMAL_FAILURE, nil
			}
		} else {
			if desiredMode == modes.MODE_DRY_RUN {
				return S_DRY_RUN_ACTIVE, nil
			} else if desiredMode == modes.MODE_STOPPED {
				return S_STOPPED, nil
			} else if desiredMode == modes.MODE_RUNNING {
				return S_NORMAL_ACTIVE, nil
			} else {
				return "", fmt.Errorf("Invalid mode %q", desiredMode)
			}
		}
	case S_NORMAL_SUCCESS:
		if err := s.successCounter.Inc(); err != nil {
			return "", err
		}
		if s.a.GetMaxRollFrequency() > time.Duration(0) {
			return S_NORMAL_SUCCESS_THROTTLED, nil
		}
		return S_NORMAL_IDLE, nil
	case S_NORMAL_SUCCESS_THROTTLED:
		if desiredMode == modes.MODE_DRY_RUN {
			return S_DRY_RUN_IDLE, nil
		} else if desiredMode == modes.MODE_STOPPED {
			return S_STOPPED, nil
		} else if s.successCounter.Get() > 0 {
			return S_NORMAL_SUCCESS_THROTTLED, nil
		}
		return S_NORMAL_IDLE, nil
	case S_NORMAL_FAILURE:
		if err := s.failCounter.Inc(); err != nil {
			return "", err
		}
		if s.a.GetNextRollRev() == s.a.GetActiveRoll().RollingTo() {
			// Rather than upload the same CL again, we'll try
			// running the CQ again after a period of throttling.
			return S_NORMAL_FAILURE_THROTTLED, nil
		}
		return S_NORMAL_IDLE, nil
	case S_NORMAL_FAILURE_THROTTLED:
		if desiredMode == modes.MODE_STOPPED {
			return S_STOPPED, nil
		} else if s.a.GetNextRollRev() != s.a.GetActiveRoll().RollingTo() {
			return S_NORMAL_IDLE, nil
		} else if desiredMode == modes.MODE_DRY_RUN {
			return S_DRY_RUN_ACTIVE, nil
		} else if s.failCounter.Get() == 0 {
			return S_NORMAL_ACTIVE, nil
		}
		return S_NORMAL_FAILURE_THROTTLED, nil
	case S_NORMAL_SAFETY_THROTTLED:
		if s.attemptCounter.Get() < s.tc.AttemptCount {
			return S_NORMAL_IDLE, nil
		} else {
			return S_NORMAL_SAFETY_THROTTLED, nil
		}
	case S_DRY_RUN_IDLE:
		if desiredMode == modes.MODE_RUNNING {
			if s.successCounter.Get() > 0 && s.a.GetMaxRollFrequency() > time.Duration(0) {
				return S_NORMAL_SUCCESS_THROTTLED, nil
			}
			return S_NORMAL_IDLE, nil
		} else if desiredMode == modes.MODE_STOPPED {
			return S_STOPPED, nil
		} else if desiredMode != modes.MODE_DRY_RUN {
			return "", fmt.Errorf("Invalid mode %q", desiredMode)
		}
		current := s.a.GetCurrentRev()
		next := s.a.GetNextRollRev()
		if current == next {
			return S_DRY_RUN_IDLE, nil
		} else if s.attemptCounter.Get() >= s.tc.AttemptCount {
			return S_DRY_RUN_SAFETY_THROTTLED, nil
		} else {
			return S_DRY_RUN_ACTIVE, nil
		}
	case S_DRY_RUN_ACTIVE:
		currentRoll := s.a.GetActiveRoll()
		if currentRoll.IsDryRunFinished() {
			if currentRoll.IsDryRunSuccess() {
				return S_DRY_RUN_SUCCESS, nil
			} else {
				return S_DRY_RUN_FAILURE, nil
			}
		} else {
			desiredMode := s.a.GetMode()
			if desiredMode == modes.MODE_RUNNING {
				return S_NORMAL_ACTIVE, nil
			} else if desiredMode == modes.MODE_STOPPED {
				return S_STOPPED, nil
			} else if desiredMode == modes.MODE_DRY_RUN {
				return S_DRY_RUN_ACTIVE, nil
			} else {
				return "", fmt.Errorf("Invalid mode %q", desiredMode)
			}
		}
	case S_DRY_RUN_SUCCESS:
		if s.a.GetNextRollRev() == s.a.GetActiveRoll().RollingTo() {
			// The current dry run is for the commit we want. Leave
			// it open so we can land it if we want.
			return S_DRY_RUN_SUCCESS_LEAVING_OPEN, nil
		}
		return S_DRY_RUN_IDLE, nil
	case S_DRY_RUN_SUCCESS_LEAVING_OPEN:
		if desiredMode == modes.MODE_RUNNING {
			return S_NORMAL_ACTIVE, nil
		} else if desiredMode == modes.MODE_STOPPED {
			return S_STOPPED, nil
		} else if desiredMode != modes.MODE_DRY_RUN {
			return "", fmt.Errorf("Invalid mode %q", desiredMode)
		}

		if s.a.GetNextRollRev() == s.a.GetActiveRoll().RollingTo() {
			// The current dry run is for the commit we want. Leave
			// it open so we can land it if we want.
			return S_DRY_RUN_SUCCESS_LEAVING_OPEN, nil
		}
		return S_DRY_RUN_IDLE, nil
	case S_DRY_RUN_FAILURE:
		if err := s.failCounter.Inc(); err != nil {
			return "", err
		}
		if s.a.GetNextRollRev() == s.a.GetActiveRoll().RollingTo() {
			// Rather than upload the same CL again, we'll try
			// running the CQ again after a period of throttling.
			return S_DRY_RUN_FAILURE_THROTTLED, nil
		}
		return S_DRY_RUN_IDLE, nil
	case S_DRY_RUN_FAILURE_THROTTLED:
		if desiredMode == modes.MODE_STOPPED {
			return S_STOPPED, nil
		} else if s.a.GetNextRollRev() != s.a.GetActiveRoll().RollingTo() {
			return S_DRY_RUN_IDLE, nil
		} else if desiredMode == modes.MODE_RUNNING {
			return S_NORMAL_ACTIVE, nil
		} else if s.failCounter.Get() == 0 {
			return S_DRY_RUN_ACTIVE, nil
		}
		return S_DRY_RUN_FAILURE_THROTTLED, nil
	case S_DRY_RUN_SAFETY_THROTTLED:
		if s.attemptCounter.Get() < s.tc.AttemptCount {
			return S_DRY_RUN_IDLE, nil
		} else {
			return S_DRY_RUN_SAFETY_THROTTLED, nil
		}
	default:
		return "", fmt.Errorf("Invalid state %q", state)
	}
}

// Attempt to perform the given state transition.
func (s *AutoRollStateMachine) Transition(ctx context.Context, dest string) error {
	fName, err := s.s.GetTransitionName(dest)
	if err != nil {
		return err
	}
	sklog.Infof("Attempting to perform transition from %q to %q: %s", s.s.Current(), dest, fName)
	if err := s.s.Transition(ctx, dest); err != nil {
		return err
	}
	sklog.Infof("Successfully performed transition.")
	return nil
}

// Attempt to perform the next state transition.
func (s *AutoRollStateMachine) NextTransition(ctx context.Context) error {
	next, err := s.GetNext()
	if err != nil {
		return err
	}
	return s.Transition(ctx, next)
}

// Perform the next state transition, plus any subsequent transitions which are
// no-ops.
func (s *AutoRollStateMachine) NextTransitionSequence(ctx context.Context) error {
	if err := s.NextTransition(ctx); err != nil {
		return err
	}
	// Greedily perform transitions until we reach a transition which is not
	// a no-op, or until we've performed a maximum number of transitions, to
	// keep us from accidentally looping extremely quickly.
	for i := 0; i < MAX_NOOP_TRANSITIONS; i++ {
		next, err := s.GetNext()
		if err != nil {
			return err
		}
		fName, err := s.s.GetTransitionName(next)
		if err != nil {
			return err
		} else if fName == F_NOOP {
			if err := s.Transition(ctx, next); err != nil {
				return err
			}
		} else {
			return nil
		}
	}
	// If we hit the maximum number of no-op transitions, there's probably
	// a bug in the state machine. Log an error but don't return it, so as
	// not to disrupt normal operation.
	sklog.Errorf("Performed %d no-op transitions in a single tick; is there a bug in the state machine?", MAX_NOOP_TRANSITIONS)
	return nil
}

// Return the current state.
func (s *AutoRollStateMachine) Current() string {
	return s.s.Current()
}
