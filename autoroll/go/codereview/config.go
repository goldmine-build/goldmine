package codereview

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.skia.org/infra/autoroll/go/config"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/github"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/util"
)

const (
	// GERRIT_CONFIG_ANDROID is a Gerrit server configuration used by
	// Android and related projects.
	GERRIT_CONFIG_ANDROID = "android"

	// GERRIT_CONFIG_ANGLE is a Gerrit server configuration used by ANGLE.
	GERRIT_CONFIG_ANGLE = "angle"

	// GERRIT_CONFIG_CHROMIUM is a Gerrit server configuration used by
	// Chromium and related projects.
	GERRIT_CONFIG_CHROMIUM = "chromium"

	// GERRIT_CONFIG_CHROMIUM_NO_CQ is a Gerrit server configuration used by
	// Chromium for projects with no Commit Queue.
	GERRIT_CONFIG_CHROMIUM_NO_CQ = "chromium-no-cq"

	// GERRIT_CONFIG_LIBASSISTANT is a Gerrit server configuration used by
	// libassistant.
	GERRIT_CONFIG_LIBASSISTANT = "libassistant"
)

var (
	// GERRIT_CONFIGS maps Gerrit config names to gerrit.Configs.
	GERRIT_CONFIGS = map[string]*gerrit.Config{
		GERRIT_CONFIG_ANDROID:        gerrit.CONFIG_ANDROID,
		GERRIT_CONFIG_ANGLE:          gerrit.CONFIG_ANGLE,
		GERRIT_CONFIG_CHROMIUM:       gerrit.CONFIG_CHROMIUM,
		GERRIT_CONFIG_CHROMIUM_NO_CQ: gerrit.CONFIG_CHROMIUM_NO_CQ,
		GERRIT_CONFIG_LIBASSISTANT:   gerrit.CONFIG_LIBASSISTANT,
	}
	cfgNameToProto = map[string]config.GerritConfig_Config{
		GERRIT_CONFIG_ANDROID:        config.GerritConfig_ANDROID,
		GERRIT_CONFIG_ANGLE:          config.GerritConfig_ANGLE,
		GERRIT_CONFIG_CHROMIUM:       config.GerritConfig_CHROMIUM,
		GERRIT_CONFIG_CHROMIUM_NO_CQ: config.GerritConfig_CHROMIUM_NO_CQ,
		GERRIT_CONFIG_LIBASSISTANT:   config.GerritConfig_LIBASSISTANT,
	}
	protoToCfgName = map[config.GerritConfig_Config]string{
		config.GerritConfig_ANDROID:        GERRIT_CONFIG_ANDROID,
		config.GerritConfig_ANGLE:          GERRIT_CONFIG_ANGLE,
		config.GerritConfig_CHROMIUM:       GERRIT_CONFIG_CHROMIUM,
		config.GerritConfig_CHROMIUM_NO_CQ: GERRIT_CONFIG_CHROMIUM_NO_CQ,
		config.GerritConfig_LIBASSISTANT:   GERRIT_CONFIG_LIBASSISTANT,
	}
)

// CodeReviewConfig provides generalized configuration information for a code
// review service.
type CodeReviewConfig interface {
	util.Validator

	// Init creates a CodeReview object based on this CodeReviewConfig.
	Init(gerrit.GerritInterface, *github.GitHub) (CodeReview, error)
}

// GerritConfig provides configuration for Gerrit.
type GerritConfig struct {
	// Gerrit host URL.
	URL string `json:"url"`

	// Project name for uploaded CLs.
	Project string `json:"project"`

	// Gerrit instance configuration. This informs the roller which labels
	// to set, among other things. The value should be one of the
	// GERRIT_CONFIG_ constants in this package.
	Config string `json:"config"`
}

// GerritConfigToProto converts a GerritConfig to a config.GerritConfig.
func GerritConfigToProto(cfg *GerritConfig) *config.GerritConfig {
	return &config.GerritConfig{
		Url:     cfg.URL,
		Project: cfg.Project,
		Config:  cfgNameToProto[cfg.Config],
	}
}

// ProtoToGerritConfig converts a config.GerritConfig to a GerritConfig.
func ProtoToGerritConfig(cfg *config.GerritConfig) *GerritConfig {
	return &GerritConfig{
		URL:     cfg.Url,
		Project: cfg.Project,
		Config:  protoToCfgName[cfg.Config],
	}
}

// Validate implements util.Validator.
func (c *GerritConfig) Validate() error {
	if c.URL == "" {
		return errors.New("URL is required.")
	}
	if c.Project == "" {
		return errors.New("Project is required.")
	}
	if _, ok := GERRIT_CONFIGS[c.Config]; !ok {
		validConfigs := make([]string, 0, len(GERRIT_CONFIGS))
		for name := range GERRIT_CONFIGS {
			validConfigs = append(validConfigs, name)
		}
		sort.Strings(validConfigs)
		return fmt.Errorf("Config must be one of: [%s]", strings.Join(validConfigs, ", "))
	}
	return nil
}

// Init implements CodeReviewConfig.
func (c *GerritConfig) Init(gerritClient gerrit.GerritInterface, githubClient *github.GitHub) (CodeReview, error) {
	return newGerritCodeReview(c, gerritClient)
}

// GetConfig returns the gerrit.Config referenced by the GerritConfig.
func (c *GerritConfig) GetConfig() (*gerrit.Config, error) {
	cfg, ok := GERRIT_CONFIGS[c.Config]
	if !ok {
		return nil, skerr.Fmt("Unknown Gerrit config %q", c.Config)
	}
	return cfg, nil
}

// CanQueryTrybots returns true if we can query for trybot results.
func (c *GerritConfig) CanQueryTrybots() bool {
	return c.Config != GERRIT_CONFIG_ANDROID
}

// GithubConfig provides configuration for Github.
type GithubConfig struct {
	RepoOwner string `json:"repoOwner,omitempty"`
	RepoName  string `json:"repoName,omitempty"`
	// If these checks are failing then we wait for them to succeed (eg: tree-status checks).
	// Note: These checks are ignored during dry runs because the PR is not going to be submitted
	// so the tree-status checks will not be important in that case.
	ChecksWaitFor []string `json:"checksWaitFor,omitempty"`
}

// GithubConfigToProto converts a GithubConfig to a config.GitHubConfig.
func GithubConfigToProto(cfg *GithubConfig) *config.GitHubConfig {
	return &config.GitHubConfig{
		RepoOwner:     cfg.RepoOwner,
		RepoName:      cfg.RepoName,
		ChecksWaitFor: cfg.ChecksWaitFor,
	}
}

// ProtoToGithubConfig converts a config.GitHubConfig to a GithubConfig.
func ProtoToGithubConfig(cfg *config.GitHubConfig) *GithubConfig {
	return &GithubConfig{
		RepoOwner:     cfg.RepoOwner,
		RepoName:      cfg.RepoName,
		ChecksWaitFor: cfg.ChecksWaitFor,
	}
}

// Validate implements util.Validator.
func (c *GithubConfig) Validate() error {
	if c.RepoOwner == "" {
		return errors.New("RepoOwner is required.")
	}
	if c.RepoName == "" {
		return errors.New("RepoName is required.")
	}
	return nil
}

// Init implements CodeReviewConfig.
func (c *GithubConfig) Init(gerritClient gerrit.GerritInterface, githubClient *github.GitHub) (CodeReview, error) {
	return newGithubCodeReview(c, githubClient)
}

// Google3Config is an empty configuration object for Google3.
type Google3Config struct{}

// Google3ConfigToProto converts a Google3Config to a config.Google3Config.
func Google3ConfigToProto(cfg *Google3Config) *config.Google3Config {
	return &config.Google3Config{}
}

// ProtoToGoogle3Config converts a config.Google3Config to a Google3Config.
func ProtoToGoogle3Config(cfg *config.Google3Config) *Google3Config {
	return &Google3Config{}
}

// Validate implements util.Validator.
func (c *Google3Config) Validate() error {
	return nil
}

// Init implements CodeReviewConfig.
func (c *Google3Config) Init(gerrit.GerritInterface, *github.GitHub) (CodeReview, error) {
	return nil, errors.New("Init not implemented for Google3Config.")
}
