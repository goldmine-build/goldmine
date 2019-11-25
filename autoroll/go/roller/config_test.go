package roller

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flynn/json5"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/autoroll/go/codereview"
	"go.skia.org/infra/autoroll/go/repo_manager"
	"go.skia.org/infra/go/deepequal"
	"go.skia.org/infra/go/notifier"
	"go.skia.org/infra/go/testutils/unittest"
)

// validBaseConfig returns a minimal valid AutoRollerConfig.
func validBaseConfig() *AutoRollerConfig {
	return &AutoRollerConfig{
		ChildName:       "childName",
		Contacts:        []string{"me@gmail.com"},
		ParentName:      "parentName",
		ParentWaterfall: "parentWaterfall",
		RollerName:      "test-roller",
		ServiceAccount:  "test-account@google.com",
		Sheriff:         []string{"sheriff@gmail.com"},
		Gerrit: &codereview.GerritConfig{
			URL:     "https://gerrit",
			Project: "my/project",
			Config:  codereview.GERRIT_CONFIG_CHROMIUM,
		},

		// Use the fake Google3 repo manager config, so that we don't
		// have to bother with correctly filling in real configs.
		Google3RepoManager: &Google3FakeRepoManagerConfig{
			ChildBranch: "master",
			ChildRepo:   "my-repo",
		},
		Kubernetes: &KubernetesConfig{
			CPU:    "1",
			Memory: "2Gb",
			Disk:   "10Gb",
		},
	}
}

func TestConfigs(t *testing.T) {
	unittest.SmallTest(t)

	// Sanity check: ensure that the base config is valid.
	require.NoError(t, validBaseConfig().Validate())

	// Helper function: create a valid base config, allow the caller to
	// mutate it, then assert that validation fails with the given message.
	testErr := func(fn func(c *AutoRollerConfig), err string) {
		c := validBaseConfig()
		fn(c)
		require.EqualError(t, c.Validate(), err)
	}

	// Test cases.

	testErr(func(c *AutoRollerConfig) {
		c.ChildName = ""
	}, "ChildName is required.")

	testErr(func(c *AutoRollerConfig) {
		c.Contacts = []string{}
	}, "At least one contact is required.")

	testErr(func(c *AutoRollerConfig) {
		c.Gerrit = nil
	}, "Exactly one of Gerrit, Github, or Google3Review is required.")

	testErr(func(c *AutoRollerConfig) {
		c.ParentName = ""
	}, "ParentName is required.")

	testErr(func(c *AutoRollerConfig) {
		c.ParentWaterfall = ""
	}, "ParentWaterfall is required.")

	testErr(func(c *AutoRollerConfig) {
		c.Sheriff = nil
	}, "Sheriff is required.")

	testErr(func(c *AutoRollerConfig) {
		c.Google3RepoManager = nil
	}, "Exactly one repo manager is expected but got 0. At config.go:335 config.go:251 config_test.go:58 config_test.go:87 testing.go:865 asm_amd64.s:1337")

	testErr(func(c *AutoRollerConfig) {
		c.AndroidRepoManager = &repo_manager.AndroidRepoManagerConfig{}
	}, "Exactly one repo manager is expected but got 2. At config.go:335 config.go:251 config_test.go:58 config_test.go:91 testing.go:865 asm_amd64.s:1337")

	testErr(func(c *AutoRollerConfig) {
		c.Notifiers = []*notifier.Config{
			{
				Filter: "debug",
			},
		}
	}, "Exactly one notification config must be supplied, but got 0")

	testErr(func(c *AutoRollerConfig) {
		c.Kubernetes = nil
	}, "Kubernetes config is required.")

	testErr(func(c *AutoRollerConfig) {
		c.Kubernetes.CPU = ""
	}, "KubernetesConfig validation failed: CPU is required.")

	testErr(func(c *AutoRollerConfig) {
		c.Kubernetes.Memory = ""
	}, "KubernetesConfig validation failed: Memory is required.")

	testErr(func(c *AutoRollerConfig) {
		c.Kubernetes.Disk = ""
	}, "kubernetes.disk is required for repo managers which use a checkout.")

	testErr(func(c *AutoRollerConfig) {
		c.Google3RepoManager = nil
		c.NoCheckoutDEPSRepoManager = &repo_manager.NoCheckoutDEPSRepoManagerConfig{
			NoCheckoutRepoManagerConfig: repo_manager.NoCheckoutRepoManagerConfig{
				CommonRepoManagerConfig: repo_manager.CommonRepoManagerConfig{
					ChildBranch:  "master",
					ChildPath:    "child",
					ParentBranch: "master",
					ParentRepo:   "fake",
				},
			},
			ChildRepo: "fake",
		}
	}, "kubernetes.disk is not valid for no-checkout repo managers.")

	// Helper function: create a valid base config, allow the caller to
	// mutate it, then assert that validation succeeds.
	testNoErr := func(fn func(c *AutoRollerConfig)) {
		c := validBaseConfig()
		fn(c)
		require.NoError(t, c.Validate())
	}

	// Test cases.

	testNoErr(func(c *AutoRollerConfig) {
		c.CqExtraTrybots = []string{"extra-bot"}
	})

	testNoErr(func(c *AutoRollerConfig) {
		c.MaxRollFrequency = "1h"
	})

	testNoErr(func(c *AutoRollerConfig) {
		c.Notifiers = []*notifier.Config{
			{
				Filter:  "debug",
				Subject: "Override Subject",
				Email: &notifier.EmailNotifierConfig{
					Emails: []string{"me@example.com"},
				},
			},
			{
				Filter: "warning",
				Chat: &notifier.ChatNotifierConfig{
					RoomID: "dummy-room",
				},
			},
		}
	})

	testNoErr(func(c *AutoRollerConfig) {
		c.SafetyThrottle = &ThrottleConfig{
			AttemptCount: 5,
			TimeWindow:   time.Hour,
		}
	})

	testNoErr(func(c *AutoRollerConfig) {
		c.Gerrit = nil
		c.Github = &codereview.GithubConfig{
			RepoOwner:      "me",
			RepoName:       "my-repo",
			ChecksNum:      3,
			ChecksWaitFor:  []string{"a", "b", "c"},
			MergeMethodURL: "???",
		}
	})
}

func TestConfigSerialization(t *testing.T) {
	unittest.SmallTest(t)

	a := validBaseConfig()

	test := func() {
		var b AutoRollerConfig
		bytes, err := json.Marshal(a)
		require.NoError(t, err)
		require.NoError(t, json5.Unmarshal(bytes, &b))
		deepequal.AssertDeepEqual(t, a, &b)
	}

	test()

	a.CqExtraTrybots = []string{"extra-bot"}
	a.MaxRollFrequency = "1h"
	a.Notifiers = []*notifier.Config{
		{
			Filter:  "debug",
			Subject: "Override Subject",
			Email: &notifier.EmailNotifierConfig{
				Emails: []string{"me@example.com"},
			},
		},
		{
			Filter: "warning",
			Chat: &notifier.ChatNotifierConfig{
				RoomID: "dummy-room",
			},
		},
	}
	a.SafetyThrottle = &ThrottleConfig{
		AttemptCount: 5,
		TimeWindow:   time.Hour,
	}

	test()
}
