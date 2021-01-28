package commit_msg

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.skia.org/infra/autoroll/go/config"
	"go.skia.org/infra/go/testutils/unittest"
)

func TestNamedTemplateAndroid_AllFeatures(t *testing.T) {
	unittest.SmallTest(t)

	b := fakeBuilder(t)
	b.cfg.Template = &config.CommitMsgConfig_BuiltIn_{
		BuiltIn: config.CommitMsgConfig_ANDROID,
	}
	result, err := b.Build(FakeCommitMsgInputs())
	require.NoError(t, err)
	require.Equal(t, `Roll fake/child/src from aaaaaaaaaaaa to cccccccccccc (2 revisions)

https://fake-child-log/aaaaaaaaaaaa..cccccccccccc

2020-04-17 c@google.com Commit C
2020-04-16 b@google.com Commit B

Also rolling transitive DEPS:
  parent/dep1 from dddddddddddd to eeeeeeeeeeee
  parent/dep3 from aaaaaaaaaaaa to cccccccccccc

If this roll has caused a breakage, revert this CL and stop the roller
using the controls here:
https://fake.server.com/r/fake-autoroll
Please CC reviewer@google.com on the revert to ensure that a human
is aware of the problem.

To report a problem with the AutoRoller itself, please file a bug:
https://bugs.chromium.org/p/skia/issues/entry?template=Autoroller+Bug

Documentation for the AutoRoller is here:
https://skia.googlesource.com/buildbot/+doc/master/autoroll/README.md

Tbr: reviewer@google.com
Test: Presubmit checks will test this change.
Exempt-From-Owner-Approval: The autoroll bot does not require owner approval.
Bug: fakebugproject:1234
Bug: fakebugproject:5678
Test: some-test
`, result)
}

func TestNamedTemplateAndroid_NoLog(t *testing.T) {
	unittest.SmallTest(t)

	b := fakeBuilder(t)
	b.cfg.Template = &config.CommitMsgConfig_BuiltIn_{
		BuiltIn: config.CommitMsgConfig_ANDROID,
	}
	b.cfg.IncludeLog = false
	result, err := b.Build(FakeCommitMsgInputs())
	require.NoError(t, err)
	require.Equal(t, `Roll fake/child/src from aaaaaaaaaaaa to cccccccccccc (2 revisions)

https://fake-child-log/aaaaaaaaaaaa..cccccccccccc

Also rolling transitive DEPS:
  parent/dep1 from dddddddddddd to eeeeeeeeeeee
  parent/dep3 from aaaaaaaaaaaa to cccccccccccc

If this roll has caused a breakage, revert this CL and stop the roller
using the controls here:
https://fake.server.com/r/fake-autoroll
Please CC reviewer@google.com on the revert to ensure that a human
is aware of the problem.

To report a problem with the AutoRoller itself, please file a bug:
https://bugs.chromium.org/p/skia/issues/entry?template=Autoroller+Bug

Documentation for the AutoRoller is here:
https://skia.googlesource.com/buildbot/+doc/master/autoroll/README.md

Tbr: reviewer@google.com
Test: Presubmit checks will test this change.
Exempt-From-Owner-Approval: The autoroll bot does not require owner approval.
Bug: fakebugproject:1234
Bug: fakebugproject:5678
Test: some-test
`, result)
}

func TestNamedTemplateAndroid_NoBugs(t *testing.T) {
	unittest.SmallTest(t)

	b := fakeBuilder(t)
	b.cfg.BugProject = ""
	b.cfg.Template = &config.CommitMsgConfig_BuiltIn_{
		BuiltIn: config.CommitMsgConfig_ANDROID,
	}
	result, err := b.Build(FakeCommitMsgInputs())
	require.NoError(t, err)
	require.Equal(t, `Roll fake/child/src from aaaaaaaaaaaa to cccccccccccc (2 revisions)

https://fake-child-log/aaaaaaaaaaaa..cccccccccccc

2020-04-17 c@google.com Commit C
2020-04-16 b@google.com Commit B

Also rolling transitive DEPS:
  parent/dep1 from dddddddddddd to eeeeeeeeeeee
  parent/dep3 from aaaaaaaaaaaa to cccccccccccc

If this roll has caused a breakage, revert this CL and stop the roller
using the controls here:
https://fake.server.com/r/fake-autoroll
Please CC reviewer@google.com on the revert to ensure that a human
is aware of the problem.

To report a problem with the AutoRoller itself, please file a bug:
https://bugs.chromium.org/p/skia/issues/entry?template=Autoroller+Bug

Documentation for the AutoRoller is here:
https://skia.googlesource.com/buildbot/+doc/master/autoroll/README.md

Tbr: reviewer@google.com
Test: Presubmit checks will test this change.
Exempt-From-Owner-Approval: The autoroll bot does not require owner approval.
Test: some-test
`, result)
}

func TestNamedTemplateAndroid_Minimal(t *testing.T) {
	unittest.SmallTest(t)

	b := fakeBuilder(t)
	b.cfg.BugProject = ""
	b.cfg.Template = &config.CommitMsgConfig_BuiltIn_{
		BuiltIn: config.CommitMsgConfig_ANDROID,
	}
	b.cfg.ChildLogUrlTmpl = ""
	b.cfg.CqExtraTrybots = nil
	b.cfg.IncludeLog = false
	b.cfg.IncludeTbrLine = false
	b.cfg.IncludeTests = false
	b.transitiveDeps = nil
	result, err := b.Build(FakeCommitMsgInputs())
	require.NoError(t, err)
	require.Equal(t, `Roll fake/child/src from aaaaaaaaaaaa to cccccccccccc (2 revisions)

If this roll has caused a breakage, revert this CL and stop the roller
using the controls here:
https://fake.server.com/r/fake-autoroll
Please CC reviewer@google.com on the revert to ensure that a human
is aware of the problem.

To report a problem with the AutoRoller itself, please file a bug:
https://bugs.chromium.org/p/skia/issues/entry?template=Autoroller+Bug

Documentation for the AutoRoller is here:
https://skia.googlesource.com/buildbot/+doc/master/autoroll/README.md

Test: Presubmit checks will test this change.
Exempt-From-Owner-Approval: The autoroll bot does not require owner approval.
`, result)
}
