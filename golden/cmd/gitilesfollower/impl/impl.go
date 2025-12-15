package impl

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/cockroachdb/cockroach-go/v2/crdb/crdbpgx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"go.goldmine.build/go/git/provider"
	"go.goldmine.build/go/git/provider/providers"
	"go.goldmine.build/go/metrics2"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/sql/sqlutil"
	"go.goldmine.build/go/util"
	"go.goldmine.build/go/vcsinfo"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/sql"
	"go.goldmine.build/golden/go/sql/schema"
	"go.opencensus.io/trace"
)

const (
	// The initial commit will be given this commit ID. Subsequent commits will have monotonically
	// increasing integers as IDs. We pick this number instead of zero in case we need to go
	// backwards, we can assign a non-negative integer as an id (which won't break the sort order
	// when turned into a string).
	initialID = 1_000_000_000
)

// CheckForNewCommitsFunc is a function that checks for new commits in the repo being tracked.
type CheckForNewCommitsFunc func(ctx context.Context) error

// StartGitFollower sets up and starts the gitiles follower. It returns a function that can be called to
// manually check for new commits outside of the normal polling cycle.
func StartGitFollower(ctx context.Context, cfg config.Common, db *pgxpool.Pool) (CheckForNewCommitsFunc, error) {
	gitp, err := providers.New(ctx, cfg.Provider, cfg.GitRepoURL, cfg.GitRepoBranch, cfg.RepoFollowerConfig.InitialCommit, cfg.GitAuthType, "", cfg.CodeReviewSystems[0].GitHubCredPath)
	if err != nil {
		sklog.Fatalf("Could not set up git provider: %s", err)
	}

	checkForNewCommits := func(ctx context.Context) error {
		return UpdateCycle(ctx, db, gitp, cfg)
	}

	// This starts a goroutine in the background
	return checkForNewCommits, pollRepo(ctx, db, gitp, cfg)
}

// pollRepo does an initial updateCycle and starts a goroutine to continue updating according
// to the provided duration for as long as the context remains ok.
func pollRepo(ctx context.Context, db *pgxpool.Pool, gitp provider.Provider, cfg config.Common) error {
	sklog.Infof("Doing initial update")
	err := UpdateCycle(ctx, db, gitp, cfg)
	if err != nil {
		return skerr.Wrap(err)
	}
	liveness := metrics2.NewLiveness("gitfollower_poll")
	go func() {
		ct := time.NewTicker(cfg.RepoFollowerConfig.PollPeriod.Duration)
		defer ct.Stop()
		sklog.Infof("Polling every %s", cfg.RepoFollowerConfig.PollPeriod.Duration)
		for {
			select {
			case <-ctx.Done():
				sklog.Errorf("Stopping polling due to context error: %s", ctx.Err())
				return
			case <-ct.C:
				err := UpdateCycle(ctx, db, gitp, cfg)
				if err != nil {
					sklog.Errorf("Error on this cycle for talking to %s: %s", cfg.GitRepoURL, err)
				} else {
					liveness.Reset()
				}
			}
		}
	}()
	return nil
}

// UpdateCycle polls the gitiles repo for the latest commit and the database for the previously
// seen commit. If those are different, it polls gitiles for all commits that happened between
// those two points and stores them to the DB.
func UpdateCycle(ctx context.Context, db *pgxpool.Pool, gitp provider.Provider, cfg config.Common) error {
	ctx, span := trace.StartSpan(ctx, "gitilesfollower_updateCycle")
	defer span.End()

	previousHash, previousID, err := getPreviousCommitFromDB(ctx, db)
	if err != nil {
		return skerr.Wrapf(err, "getting recent commits from DB")
	}

	if previousHash == "" {
		sklog.Infof("No previous commits in DB, starting from initial commit %q", cfg.RepoFollowerConfig.InitialCommit)
		previousHash = cfg.RepoFollowerConfig.InitialCommit
	}

	if previousID == 0 {
		previousID = initialID
	}

	commits := []*vcsinfo.LongCommit{}
	err = gitp.CommitsFromMostRecentGitHashToHead(ctx, previousHash, func(c provider.Commit) error {
		lc := &vcsinfo.LongCommit{
			ShortCommit: &vcsinfo.ShortCommit{},
		}
		lc.ShortCommit.Hash = c.GitHash
		lc.ShortCommit.Author = c.Author
		lc.ShortCommit.Subject = c.Subject
		lc.Body = c.Body
		lc.Timestamp = time.Unix(c.Timestamp, 0)
		commits = append(commits, lc)
		return nil
	})
	if err != nil {
		return skerr.Wrapf(err, "getting commits from git provider")
	}

	if len(commits) == 0 {
		sklog.Infof("No new commits since last seen commit %q", previousHash)
		return nil
	}

	if err := storeCommits(ctx, db, previousID, commits); err != nil {
		return skerr.Wrapf(err, "storing %d commits to GitCommits table", len(commits))
	}

	if err := checkForCommitsWithExpectations(ctx, db, commits, cfg); err != nil {
		return skerr.Wrapf(err, "checking for commits with expectations")
	}

	return nil
}

func checkForCommitsWithExpectations(ctx context.Context, db *pgxpool.Pool, commits []*vcsinfo.LongCommit, cfg config.Common) error {
	sklog.Infof("Found %d commits to check for a CL", len(commits))
	for _, c := range commits {
		var clID string
		switch cfg.RepoFollowerConfig.ExtractionTechnique {
		case config.ReviewedLine:
			clID = ExtractReviewedLine(c.Body)
		case config.FromSubject:
			clID = ExtractFromSubject(c.Subject)
		}
		if clID == "" {
			sklog.Infof("No CL detected for %#v", c)
			continue
		}
		if err := migrateExpectationsToPrimaryBranch(ctx, db, cfg.RepoFollowerConfig.SystemName, clID, c.Timestamp, !cfg.RepoFollowerConfig.LegacyUpdaterInUse); err != nil {
			return skerr.Wrapf(err, "migrating cl %s-%s", cfg.RepoFollowerConfig.SystemName, clID)
		}
		sklog.Infof("Commit %s landed at %s", c.Hash[:12], c.Timestamp)
	}
	_, err := db.Exec(ctx, `UPSERT INTO TrackingCommits (repo, last_git_hash) VALUES ($1, $2)`, cfg.GitRepoURL, commits[len(commits)-1].Hash)
	return skerr.Wrap(err)
}

// getPreviousCommitFromDB returns the git_hash and the commit_id of the most recently stored
// commit. "Most recent" here is defined by the lexicographical order of the commit_id. Of note,
// commit_id is returned as an integer because subsequent ids will be computed by adding to that
// integer value.
//
// This approach takes a lesson from Perf by only querying data from the most recent commit in the
// DB and the latest on the tree to make Gold resilient to merged/changed history.
// (e.g. go/skia-infra-pm-007)
func getPreviousCommitFromDB(ctx context.Context, db *pgxpool.Pool) (string, int64, error) {
	ctx, span := trace.StartSpan(ctx, "gitilesfollower_getPreviousCommitFromDB")
	defer span.End()
	row := db.QueryRow(ctx, `SELECT git_hash, commit_id FROM GitCommits
ORDER BY commit_id DESC LIMIT 1`)
	hash := ""
	id := ""
	if err := row.Scan(&hash, &id); err != nil {
		if err == pgx.ErrNoRows {
			return "", 0, nil // No data in GitCommits
		}
		return "", 0, skerr.Wrap(err)
	}
	idInt, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", 0, skerr.Wrapf(err, "It is assumed that the commit ids for this type of repo tracking are ints: %q", id)
	}
	return hash, idInt, nil
}

// storeCommits writes the given commits to the SQL database, assigning them commitIDs in
// monotonically increasing order. The commits slice is expected to be sorted with the oldest
// commit first (the opposite of how gitiles returns it).
func storeCommits(ctx context.Context, db *pgxpool.Pool, lastCommitID int64, commits []*vcsinfo.LongCommit) error {
	ctx, span := trace.StartSpan(ctx, "gitilesfollower_storeCommits")
	defer span.End()
	commitID := lastCommitID + 1
	// batchSize is only really relevant in the initial load. But we need it to avoid going over
	// the 65k limit of placeholder indexes.
	const batchSize = 1000
	const statement = `UPSERT INTO GitCommits (git_hash, commit_id, commit_time, author_email, subject) VALUES `
	const valuesPerRow = 5
	err := util.ChunkIter(len(commits), batchSize, func(startIdx int, endIdx int) error {
		chunk := commits[startIdx:endIdx]
		arguments := make([]interface{}, 0, len(chunk)*valuesPerRow)
		for _, c := range chunk {
			cid := fmt.Sprintf("%012d", commitID)
			arguments = append(arguments, c.Hash, cid, c.Timestamp, c.Author, c.Subject)
			commitID++
		}
		vp := sqlutil.ValuesPlaceholders(valuesPerRow, len(chunk))
		if _, err := db.Exec(ctx, statement+vp, arguments...); err != nil {
			return skerr.Wrap(err)
		}
		return nil
	})
	return skerr.Wrap(err)
}

var reviewedLineRegex = regexp.MustCompile(`(^|\n)Reviewed-on: .+/(?P<clID>\S+?)($|\n)`)

// ExtractReviewedLine looks for a line that starts with Reviewed-on and then parses out the
// CL id from that line (which are the last characters after the last slash).
func ExtractReviewedLine(clBody string) string {
	match := reviewedLineRegex.FindStringSubmatch(clBody)
	if len(match) > 0 {
		return match[2] // the second group should be our CL ID
	}
	return ""
}

// We assume a PR has the pull request number in the Subject/Title, at the end.
// e.g. "Turn off docs upload temporarily (#44365) (#44413)" refers to PR 44413
var prSuffix = regexp.MustCompile(`.+\(#(?P<id>\d+)\)\s*$`)

// ExtractFromSubject looks at the subject of a CL and expects to find the associated CL (aka Pull
// Request) appended to the message.
func ExtractFromSubject(subject string) string {
	if match := prSuffix.FindStringSubmatch(subject); match != nil {
		// match[0] is the whole string, match[1] is the first group
		return match[1]
	}
	return ""
}

// migrateExpectationsToPrimaryBranch finds all the expectations that were added for a given CL
// and condenses them into one record per user who triaged digests on that CL. These records are
// all stored with the same timestamp as the commit that landed with them. The records and their
// corresponding expectations are added to the primary branch. Then the given CL is marked as
// "landed".
func migrateExpectationsToPrimaryBranch(ctx context.Context, db *pgxpool.Pool, crs, clID string, landedTS time.Time, setLanded bool) error {
	ctx, span := trace.StartSpan(ctx, "migrateExpectationsToPrimaryBranch")
	defer span.End()
	qID := sql.Qualify(crs, clID)
	changes, err := getExpectationChangesForCL(ctx, db, qID)
	if err != nil {
		return skerr.Wrap(err)
	}
	sklog.Infof("CL %s %s had %d expectations, which will be applied at %s", crs, clID, len(changes), landedTS)
	err = storeChangesAsRecordDeltasExpectations(ctx, db, changes, landedTS)
	if err != nil {
		return skerr.Wrap(err)
	}
	if setLanded {
		row := db.QueryRow(ctx, `UPDATE Changelists SET status = 'landed' WHERE changelist_id = $1 RETURNING changelist_id`, qID)
		var s string
		if err := row.Scan(&s); err != nil {
			if err == pgx.ErrNoRows {
				return nil
			}
			return skerr.Wrapf(err, "Updating cl %s to be landed", qID)
		}
	} else {
		sklog.Infof("Not marking CL %s %s as landed [legacy mode in use] ", crs, clID)
	}
	return nil
}

type groupingDigest struct {
	grouping schema.MD5Hash
	digest   schema.MD5Hash
}

type finalState struct {
	labelBefore        schema.ExpectationLabel
	labelAfter         schema.ExpectationLabel
	userWhoTriagedLast string
}

// getExpectationChangesForCL gets all the expectations for the given CL and arranges them in
// temporal order. It de-duplicates any entries (e.g. triaging a digest to positive,then to
// negative, then to positive would be condensed to a single "triage to positive" action). Entries
// are "blamed" to the user who last touched the digest+grouping pair.
func getExpectationChangesForCL(ctx context.Context, db *pgxpool.Pool, qualifiedCLID string) (map[groupingDigest]finalState, error) {
	ctx, span := trace.StartSpan(ctx, "getExpectationChangesForCL")
	defer span.End()
	rows, err := db.Query(ctx, `
SELECT user_name, grouping_id, digest, label_before, label_after
FROM ExpectationRecords JOIN ExpectationDeltas
  ON ExpectationRecords.expectation_record_id = ExpectationDeltas.expectation_record_id
WHERE branch_name = $1
ORDER BY triage_time ASC`, qualifiedCLID)
	if err != nil {
		return nil, skerr.Wrapf(err, "Getting deltas and records for CL %s", qualifiedCLID)
	}
	defer rows.Close()
	// By using a map, we can deduplicate rows and return an object that represents the final
	// state of all the triage logic.
	rv := map[groupingDigest]finalState{}
	for rows.Next() {
		var user string
		var grouping schema.GroupingID
		var digest schema.DigestBytes
		var before schema.ExpectationLabel
		var after schema.ExpectationLabel
		if err := rows.Scan(&user, &grouping, &digest, &before, &after); err != nil {
			return nil, skerr.Wrap(err)
		}
		key := groupingDigest{
			grouping: sql.AsMD5Hash(grouping),
			digest:   sql.AsMD5Hash(digest),
		}
		fs, ok := rv[key]
		if !ok {
			// only update the label before on the first time we see a triage for a grouping.
			fs.labelBefore = before
		}
		fs.labelAfter = after
		fs.userWhoTriagedLast = user
		rv[key] = fs
	}
	return rv, nil
}

// storeChangesAsRecordDeltasExpectations takes the given map and turns them into ExpectationDeltas.
// From there, it is able to make a record per user and store the given deltas and expectations
// according to that record.
func storeChangesAsRecordDeltasExpectations(ctx context.Context, db *pgxpool.Pool, changes map[groupingDigest]finalState, ts time.Time) error {
	ctx, span := trace.StartSpan(ctx, "storeChangesAsRecordDeltasExpectations")
	defer span.End()
	if len(changes) == 0 {
		return nil
	}
	// We want to make one triage record for each user who triaged data on this CL. Those records
	// will represent the final state.
	byUser := map[string][]schema.ExpectationDeltaRow{}
	for gd, fs := range changes {
		if fs.labelBefore == fs.labelAfter {
			continue // skip "no-op" triages, where something was triaged in one way, then undone.
		}
		byUser[fs.userWhoTriagedLast] = append(byUser[fs.userWhoTriagedLast], schema.ExpectationDeltaRow{
			GroupingID:  sql.FromMD5Hash(gd.grouping),
			Digest:      sql.FromMD5Hash(gd.digest),
			LabelBefore: fs.labelBefore,
			LabelAfter:  fs.labelAfter,
		})
	}
	for user, deltas := range byUser {
		if len(deltas) == 0 {
			continue
		}
		recordID := uuid.New()
		// Write the record for this user
		err := crdbpgx.ExecuteTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
INSERT INTO ExpectationRecords (expectation_record_id, user_name, triage_time, num_changes)
VALUES ($1, $2, $3, $4)`, recordID, user, ts, len(deltas))
			return err // Don't wrap - crdbpgx might retry
		})
		if err != nil {
			return skerr.Wrapf(err, "storing record")
		}
		if err := bulkWriteDeltas(ctx, db, recordID, deltas); err != nil {
			return skerr.Wrapf(err, "storing deltas")
		}
		if err := bulkWriteExpectations(ctx, db, recordID, deltas); err != nil {
			return skerr.Wrapf(err, "storing expectations")
		}
	}
	return nil
}

// bulkWriteDeltas stores all the deltas using a batched approach. They are all attributed to the
// provided record id.
func bulkWriteDeltas(ctx context.Context, db *pgxpool.Pool, recordID uuid.UUID, deltas []schema.ExpectationDeltaRow) error {
	ctx, span := trace.StartSpan(ctx, "bulkWriteDeltas")
	defer span.End()
	const chunkSize = 200 // Arbitrarily picked
	err := util.ChunkIter(len(deltas), chunkSize, func(startIdx int, endIdx int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := deltas[startIdx:endIdx]
		if len(batch) == 0 {
			return nil
		}
		statement := `INSERT INTO ExpectationDeltas (expectation_record_id, grouping_id, digest,
label_before, label_after) VALUES `
		const valuesPerRow = 5
		statement += sqlutil.ValuesPlaceholders(valuesPerRow, len(batch))
		arguments := make([]interface{}, 0, valuesPerRow*len(batch))
		for _, row := range batch {
			arguments = append(arguments, recordID, row.GroupingID, row.Digest, row.LabelBefore, row.LabelAfter)
		}
		err := crdbpgx.ExecuteTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, statement, arguments...)
			return err // Don't wrap - crdbpgx might retry
		})
		return skerr.Wrap(err)
	})
	if err != nil {
		return skerr.Wrapf(err, "storing %d expectation delta rows", len(deltas))
	}
	return nil
}

// bulkWriteExpectations stores all the expectations using a batched approach. They are all
// attributed to the provided record id.
func bulkWriteExpectations(ctx context.Context, db *pgxpool.Pool, recordID uuid.UUID, deltas []schema.ExpectationDeltaRow) error {
	ctx, span := trace.StartSpan(ctx, "bulkWriteExpectations")
	defer span.End()
	const chunkSize = 200 // Arbitrarily picked
	err := util.ChunkIter(len(deltas), chunkSize, func(startIdx int, endIdx int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := deltas[startIdx:endIdx]
		if len(batch) == 0 {
			return nil
		}
		statement := `UPSERT INTO Expectations (grouping_id, digest, label, expectation_record_id) VALUES `
		const valuesPerRow = 4
		statement += sqlutil.ValuesPlaceholders(valuesPerRow, len(batch))
		arguments := make([]interface{}, 0, valuesPerRow*len(batch))
		for _, row := range batch {
			arguments = append(arguments, row.GroupingID, row.Digest, row.LabelAfter, recordID)
		}
		err := crdbpgx.ExecuteTx(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, statement, arguments...)
			return err // Don't wrap - crdbpgx might retry
		})
		return skerr.Wrap(err)
	})
	if err != nil {
		return skerr.Wrapf(err, "storing %d expectation rows", len(deltas))
	}
	return nil
}
