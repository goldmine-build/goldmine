"""This module defines the test_on_env rule."""

def test_on_env(name, test, env, timeout_secs = 10):
    """Allows running test targets that require launching a test environment before their execution.

    The test_on_env rule takes a *_test target and an environment *_binary target as arguments,
    and carries out the following steps when invoked with "blaze test":

    1. Launches the environment binary.
    2. Waits until the environment signals that it is ready.
    3. Runs the test target.
    4. Tears down the environment process by sending it a SIGTERM signal.
    5. Reports the results of the test target (pass/fail).

    The test_on_env runner script sets two environment variables:

    - ENV_READY_FILE: Path to a "ready file" that the environment must create to signal the test
      runner that it is ready to accept connections.
    - ENV_DIR: Path to a temporary directory that can be used to communicate between the environment
      and the test binaries.

    Requirements:
    - The environment binary must create the $ENV_READY_FILE as soon as it is ready, otherwise the
      test_on_env runner will wait "forever", eventually timing out and failing.
    - Any TCP ports open by the environment binary must be chosen by the OS (i.e. no hardcoded port
      numbers), otherwise tests running in parallel might interfere with each other and cause
      non-deterministic test failures.

    Optional:
    - The $ENV_DIR directory can be used to communicate between the environment and test binaries.
      A typical use case is for the environment to create an $ENV_DIR/port file containing a TCP
      port number chosen by the OS.

    Some examples of tests that might require an environment include: Puppeteer tests, where the
    environment can be a demo page server (for screenshot tests) or a web application server (for
    integration tests); integration tests for a command-line tool that talks an RPC server, etc.

    Args:
      name: Name of the rule.
      test: Label for the test to execute (can be any *_test target).
      env: Label for the environment binary (can be any *_binary target).
      timeout_secs: Approximate maximum number of seconds to wait for the environment to be ready.
    """
    native.sh_test(
        name = name,
        srcs = ["//bazel/test_on_env:test_on_env.sh"],
        args = [
            "--test-bin $(location %s)" % test,
            "--env-bin $(location %s)" % env,
            "--ready-check-timeout-secs %d" % timeout_secs,
        ],
        data = [test, env],
    )
