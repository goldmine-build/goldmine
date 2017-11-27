/*
	A wrapper around the os/exec package that supports timeouts and testing.

	Example usage:

	Simple command with argument:
	err := Run(&Command{
		Name: "touch",
		Args: []string{file},
	})

	More complicated example:
	output := bytes.Buffer{}
	err := Run(&Command{
		Name: "make",
		Args: []string{"all"},
		// Set environment:
		Env: []string{fmt.Sprintf("GOPATH=%s", projectGoPath)},
		// Set working directory:
		Dir: projectDir,
		// Capture output:
		CombinedOutput: &output,
		// Set a timeout:
		Timeout: 10*time.Minute,
	})

	Inject a Run function for testing:
	var actualCommand *Command
	SetRunForTesting(func(command *Command) error {
		actualCommand = command
		return nil
	})
	defer SetRunForTesting(DefaultRun)
	TestCodeCallingRun()
	expect.Equal(t, "touch", actualCommand.Name)
	expect.Equal(t, 1, len(actualCommand.Args))
	expect.Equal(t, file, actualCommand.Args[0])
*/
package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
)

const (
	TIMEOUT_ERROR_PREFIX = "Command killed since it took longer than"
)

type Verbosity int

const (
	Info Verbosity = iota
	Debug
	Silent
)

var (
	contextKey     = &struct{}{}
	defaultContext = &execContext{DefaultRun}

	WriteInfoLog  = WriteLog{LogFunc: sklog.Infof}
	WriteErrorLog = WriteLog{LogFunc: sklog.Errorf}
)

// WriteLog implements the io.Writer interface and writes to the given log function.
type WriteLog struct {
	LogFunc func(format string, args ...interface{})
}

func (wl WriteLog) Write(p []byte) (n int, err error) {
	wl.LogFunc("%s", string(p))
	return len(p), nil
}

type Command struct {
	// Name of the command, as passed to osexec.Command. Can be the path to a binary or the
	// name of a command that osexec.Lookpath can find.
	Name string
	// Arguments of the command, not including Name.
	Args []string
	// The environment of the process. If nil, the current process's environment is used.
	Env []string
	// If Env is non-nil, adds the current process's entire environment to Env, excluding
	// variables that are set in Env.
	InheritEnv bool
	// If Env is non-nil, adds the current process's PATH to Env. Do not include PATH in Env.
	InheritPath bool
	// The working directory of the command. If nil, runs in the current process's current
	// directory.
	Dir string
	// See docs for osexec.Cmd.Stdin.
	Stdin io.Reader
	// If true, duplicates stdout of the command to WriteInfoLog.
	LogStdout bool
	// Sends the stdout of the command to this Writer, e.g. os.File or bytes.Buffer.
	Stdout io.Writer
	// If true, duplicates stderr of the command to WriteErrorLog.
	LogStderr bool
	// Sends the stderr of the command to this Writer, e.g. os.File or bytes.Buffer.
	Stderr io.Writer
	// Sends the combined stdout and stderr of the command to this Writer, in addition to
	// Stdout and Stderr. Only one goroutine will write at a time. Note: the Go runtime seems to
	// combine stdout and stderr into one stream as long as LogStdout and LogStderr are false
	// and Stdout and Stderr are nil. Otherwise, the stdout and stderr of the command could be
	// arbitrarily reordered when written to CombinedOutput.
	CombinedOutput io.Writer
	// Time limit to wait for the command to finish. No limit if not specified.
	Timeout time.Duration
	// Whether to log when the command starts.
	Verbose Verbosity
}

type Process interface {
	Kill() error
}

// Divides commandLine at spaces; treats the first token as the program name and the other tokens
// as arguments. Note: don't expect this function to do anything smart with quotes or escaped
// spaces.
func ParseCommand(commandLine string) Command {
	programAndArgs := strings.Split(commandLine, " ")
	return Command{Name: programAndArgs[0], Args: programAndArgs[1:]}
}

// Given io.Writers or nils, return a single writer that writes to all, or nil if no non-nil
// writers. Also checks for non-nil io.Writer containing a nil value.
// http://devs.cloudimmunity.com/gotchas-and-common-mistakes-in-go-golang/index.html#nil_in_nil_in_vals
func squashWriters(writers ...io.Writer) io.Writer {
	nonNil := []io.Writer{}
	for _, writer := range writers {
		if writer != nil && !util.IsNil(writer) {
			nonNil = append(nonNil, writer)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	default:
		return io.MultiWriter(nonNil...)
	}
}

// Returns the Env, Name, and Args of command joined with spaces. Does not perform any quoting.
func DebugString(command *Command) string {
	result := ""
	result += strings.Join(command.Env, " ")
	if len(command.Env) != 0 {
		result += " "
	}
	result += command.Name
	if len(command.Args) != 0 {
		result += " "
	}
	result += strings.Join(command.Args, " ")
	return result
}

func createCmd(command *Command) *osexec.Cmd {
	cmd := osexec.Command(command.Name, command.Args...)
	if len(command.Env) != 0 {
		cmd.Env = command.Env
		if command.InheritEnv {
			existing := make(map[string]bool, len(command.Env))
			for _, s := range command.Env {
				existing[strings.SplitN(s, "=", 2)[0]] = true
			}
			for _, s := range os.Environ() {
				if !existing[strings.SplitN(s, "=", 2)[0]] {
					cmd.Env = append(cmd.Env, s)
				}
			}
		} else if command.InheritPath {
			cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH"))
		}
	}
	cmd.Dir = command.Dir
	cmd.Stdin = command.Stdin
	var stdoutLog io.Writer
	if command.LogStdout {
		stdoutLog = WriteInfoLog
	}
	cmd.Stdout = squashWriters(stdoutLog, command.Stdout, command.CombinedOutput)
	var stderrLog io.Writer
	if command.LogStderr {
		stderrLog = WriteErrorLog
	}
	cmd.Stderr = squashWriters(stderrLog, command.Stderr, command.CombinedOutput)
	return cmd
}

func start(command *Command, cmd *osexec.Cmd) error {
	if command.Verbose != Silent {
		dirMsg := ""
		if cmd.Dir != "" {
			dirMsg = " with CWD " + cmd.Dir
		}
		if command.Verbose == Info {
			sklog.Infof("Executing '%s' (where %s is %s)%s", DebugString(command), command.Name, cmd.Path, dirMsg)
		} else if command.Verbose == Debug {
			sklog.Debugf("Executing '%s' (where %s is %s)%s", DebugString(command), command.Name, cmd.Path, dirMsg)
		}

	}
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("Unable to start command %s: %s", DebugString(command), err)
	}
	return nil
}

func waitSimple(command *Command, cmd *osexec.Cmd) error {
	err := cmd.Wait()
	if err != nil {
		return fmt.Errorf("Command exited with %s: %s", err, DebugString(command))
	}
	return nil
}

func wait(command *Command, cmd *osexec.Cmd) error {
	if command.Timeout == 0 {
		return waitSimple(command, cmd)
	}
	done := make(chan error)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-time.After(command.Timeout):
		if err := cmd.Process.Kill(); err != nil {
			return fmt.Errorf("Failed to kill timed out process: %s", err)
		}
		<-done // allow goroutine to exit
		return fmt.Errorf("%s %f secs", TIMEOUT_ERROR_PREFIX, command.Timeout.Seconds())
	case err := <-done:
		if err != nil {
			return fmt.Errorf("Command exited with %s: %s", err, DebugString(command))
		}
		return nil
	}
}

// IsTimeout returns true if the specified error was raised due to a command
// timing out.
func IsTimeout(err error) bool {
	return strings.Contains(err.Error(), TIMEOUT_ERROR_PREFIX)
}

// DefaultRun can be passed to SetRunForTesting to go back to running commands as normal.
func DefaultRun(command *Command) error {
	cmd := createCmd(command)
	if err := start(command, cmd); err != nil {
		return err
	}
	return wait(command, cmd)
}

// execContext is a struct used for controlling the execution context of Commands.
type execContext struct {
	runFn func(*Command) error
}

// WithContext returns a context.Context instance which uses the given function
// to run Commands.
func NewContext(ctx context.Context, runFn func(*Command) error) context.Context {
	newCtx := &execContext{
		runFn: runFn,
	}
	return context.WithValue(ctx, contextKey, newCtx)
}

// getCtx retrieves the Context associated with the context.Context.
func getCtx(ctx context.Context) *execContext {
	if v := ctx.Value(contextKey); v != nil {
		return v.(*execContext)
	}
	return defaultContext
}

// See documentation for exec.Run.
func (c *execContext) Run(command *Command) error {
	return c.runFn(command)
}

// runSimpleCommand executes the given command.  Returns the combined stdout and stderr. May also
// return an error if the command exited with a non-zero status or there is any other error.
func (c *execContext) runSimpleCommand(command *Command) (string, error) {
	output := bytes.Buffer{}
	command.CombinedOutput = &output
	// Setting Verbose to Silent to maintain previous behavior.
	command.Verbose = Silent
	err := c.Run(command)
	result := string(output.Bytes())
	if err != nil {
		return result, fmt.Errorf("%s; Stdout+Stderr:\n%s", err.Error(), result)
	}
	return result, nil
}

// See documentation for exec.RunSimple.
func (c *execContext) RunSimple(commandLine string) (string, error) {
	cmd := ParseCommand(commandLine)
	return c.runSimpleCommand(&cmd)
}

// See documentation for exec.RunCommand.
func (c *execContext) RunCommand(command *Command) (string, error) {
	return c.runSimpleCommand(command)
}

// See documentation for exec.RunCwd.
func (c *execContext) RunCwd(cwd string, args ...string) (string, error) {
	command := &Command{
		Name: args[0],
		Args: args[1:],
		Dir:  cwd,
	}
	return c.runSimpleCommand(command)
}

// Run runs command and waits for it to finish. If any failure, returns non-nil. If a timeout was
// specified, returns an error once the command has exceeded that timeout.
func Run(ctx context.Context, command *Command) error {
	return getCtx(ctx).Run(command)
}

// RunSimple executes the given command line string; the command being run is expected to not care
// what its current working directory is. Returns the combined stdout and stderr. May also return
// an error if the command exited with a non-zero status or there is any other error.
func RunSimple(ctx context.Context, commandLine string) (string, error) {
	return getCtx(ctx).RunSimple(commandLine)
}

// RunCommand executes the given command and returns the combined stdout and stderr. May also
// return an error if the command exited with a non-zero status or there is any other error.
func RunCommand(ctx context.Context, command *Command) (string, error) {
	return getCtx(ctx).runSimpleCommand(command)
}

// RunCwd executes the given command in the given directory. Returns the combined stdout and
// stderr. May also return an error if the command exited with a non-zero status or there is any
// other error.
func RunCwd(ctx context.Context, cwd string, args ...string) (string, error) {
	return getCtx(ctx).RunCwd(cwd, args...)
}

// RunIndefinitely starts the command and then returns. Clients can listen for
// the command to end on the returned channel or kill the process manually
// using the Process handle. The timeout param is ignored if it is set.  If
// starting the command returns an error, that error is returned.
func RunIndefinitely(command *Command) (Process, <-chan error, error) {
	cmd := createCmd(command)
	done := make(chan error)
	if err := start(command, cmd); err != nil {
		close(done)
		return nil, done, err
	}
	go func() {
		done <- cmd.Wait()
	}()
	return cmd.Process, done, nil
}
