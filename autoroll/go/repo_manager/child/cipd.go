package child

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	cipd_api "go.chromium.org/luci/cipd/client/cipd"
	"go.chromium.org/luci/cipd/common"

	"go.skia.org/infra/autoroll/go/config"
	"go.skia.org/infra/autoroll/go/revision"
	"go.skia.org/infra/go/cipd"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/go/vfs"
)

const (
	cipdPackageUrlTmpl  = "%s/p/%s/+/%s"
	cipdBuganizerPrefix = "b/"
)

var (
	cipdDetailsRegex = regexp.MustCompile(`details(\d+)`)
)

// CIPDConfig provides configuration for CIPDChild.
type CIPDConfig struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

// Validate implements util.Validator.
func (c *CIPDConfig) Validate() error {
	if c.Name == "" {
		return skerr.Fmt("Name is required.")
	}
	if c.Tag == "" {
		return skerr.Fmt("Tag is required.")
	}
	return nil
}

// CIPDConfigToProto converts a CIPDConfig to a config.CIPDChildConfig.
func CIPDConfigToProto(cfg *CIPDConfig) *config.CIPDChildConfig {
	return &config.CIPDChildConfig{
		Name: cfg.Name,
		Tag:  cfg.Tag,
	}
}

// ProtoToCIPDConfig converts a config.CIPDChildConfig to a CIPDConfig.
func ProtoToCIPDConfig(cfg *config.CIPDChildConfig) *CIPDConfig {
	return &CIPDConfig{
		Name: cfg.Name,
		Tag:  cfg.Tag,
	}
}

// NewCIPD returns an implementation of Child which deals with a CIPD package.
// If the caller calls CIPDChild.Download, the destination must be a descendant of
// the provided workdir.
func NewCIPD(ctx context.Context, c CIPDConfig, client *http.Client, workdir string) (*CIPDChild, error) {
	if err := c.Validate(); err != nil {
		return nil, skerr.Wrap(err)
	}
	cipdClient, err := cipd.NewClient(client, workdir)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	return &CIPDChild{
		client: cipdClient,
		name:   c.Name,
		root:   workdir,
		tag:    c.Tag,
	}, nil
}

// CIPDChild is an implementation of Child which deals with a CIPD package.
type CIPDChild struct {
	client cipd.CIPDClient
	name   string
	root   string
	tag    string
}

// See documentation for Child interface.
func (c *CIPDChild) GetRevision(ctx context.Context, id string) (*revision.Revision, error) {
	instance, err := c.client.Describe(ctx, c.name, id)
	if err != nil {
		return nil, err
	}
	return CIPDInstanceToRevision(c.name, instance), nil
}

// See documentation for Child interface.
// Note: that this just finds all versions of the package between the last
// rolled version and the version currently pointed to by the configured tag; we
// can't know whether the tag we're tracking was ever actually applied to any of
// the package instances in between.
func (c *CIPDChild) Update(ctx context.Context, lastRollRev *revision.Revision) (*revision.Revision, []*revision.Revision, error) {
	head, err := c.client.ResolveVersion(ctx, c.name, c.tag)
	if err != nil {
		return nil, nil, skerr.Wrap(err)
	}
	tipRev, err := c.GetRevision(ctx, head.InstanceID)
	if err != nil {
		return nil, nil, skerr.Wrap(err)
	}
	notRolledRevs := []*revision.Revision{}
	if lastRollRev.Id != tipRev.Id {
		notRolledRevs = append(notRolledRevs, tipRev)
	}
	return tipRev, notRolledRevs, nil
}

// VFS implements the Child interface.
func (c *CIPDChild) VFS(ctx context.Context, rev *revision.Revision) (vfs.FS, error) {
	fs, err := vfs.TempDir(ctx, c.root, "tmp")
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	pin := common.Pin{
		PackageName: c.name,
		InstanceID:  rev.Id,
	}
	dest, err := filepath.Rel(c.root, fs.Dir())
	if err := c.client.FetchAndDeployInstance(ctx, dest, pin, 0); err != nil {
		return nil, skerr.Wrap(err)
	}
	return fs, nil
}

// SetClientForTesting sets the CIPDClient used by the CIPDChild so that it can
// be overridden for testing.
func (c *CIPDChild) SetClientForTesting(client cipd.CIPDClient) {
	c.client = client
}

type cipdDetailsLine struct {
	index int
	line  string
}

// CIPDInstanceToRevision creates a revision.Revision based on the given
// InstanceInfo.
func CIPDInstanceToRevision(name string, instance *cipd_api.InstanceDescription) *revision.Revision {
	rev := &revision.Revision{
		Id:          instance.Pin.InstanceID,
		Author:      instance.RegisteredBy,
		Display:     util.Truncate(instance.Pin.InstanceID, 12),
		Description: instance.Pin.String(),
		Timestamp:   time.Time(instance.RegisteredTs),
		URL:         fmt.Sprintf(cipdPackageUrlTmpl, cipd.ServiceUrl, name, instance.Pin.InstanceID),
	}
	detailsLines := []*cipdDetailsLine{}
	for _, tag := range instance.Tags {
		split := strings.SplitN(tag.Tag, ":", 2)
		if len(split) != 2 {
			sklog.Errorf("Invalid CIPD tag %q; expected <key>:<value>", tag.Tag)
			continue
		}
		key := split[0]
		val := split[1]
		if key == "bug" {
			// For bugs, we expect either eg. "chromium:1234" or "b/1234".
			split := strings.SplitN(val, ":", 2)
			if rev.Bugs == nil {
				rev.Bugs = map[string][]string{}
			}
			if len(split) == 2 {
				rev.Bugs[split[0]] = append(rev.Bugs[split[0]], split[1])
			} else if strings.HasPrefix(val, cipdBuganizerPrefix) {
				rev.Bugs[util.BUG_PROJECT_BUGANIZER] = append(rev.Bugs[util.BUG_PROJECT_BUGANIZER], val[len(cipdBuganizerPrefix):])
			} else {
				sklog.Errorf("Invalid format for \"bug\" tag: %s", tag.Tag)
			}
		} else if m := cipdDetailsRegex.FindStringSubmatch(key); len(m) == 2 {
			// For details, the tag value becomes one line. The tag key includes
			// an int which is used to determine the ordering of the lines.
			index, err := strconv.Atoi(m[1])
			if err != nil {
				// This shouldn't happen thanks to the regex.
				sklog.Errorf("Failed to parse int from details tag %q: %s", tag.Tag, err)
				continue
			}
			detailsLines = append(detailsLines, &cipdDetailsLine{
				index: index,
				line:  val,
			})
		}
	}
	// Concatenate the details lines.
	if len(detailsLines) > 0 {
		sort.Slice(detailsLines, func(i, j int) bool {
			if detailsLines[i].index == detailsLines[j].index {
				return detailsLines[i].line < detailsLines[j].line
			}
			return detailsLines[i].index < detailsLines[j].index
		})
		for idx, line := range detailsLines {
			rev.Details += line.line
			if idx < len(detailsLines)-1 {
				rev.Details += "\n"
			}
		}

	}
	return rev
}

var _ Child = &CIPDChild{}
