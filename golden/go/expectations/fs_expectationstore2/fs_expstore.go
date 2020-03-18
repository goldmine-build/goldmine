package fs_expectationstore

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/sync/errgroup"

	ifirestore "go.skia.org/infra/go/firestore"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/golden/go/expectations"
	"go.skia.org/infra/golden/go/fs_utils"
	"go.skia.org/infra/golden/go/types"
)

const (
	partitions         = "expstore_partitions_v3"
	expectationEntries = "entries"
	recordEntries      = "triage_records"
	changeEntries      = "triage_changes"

	masterPartition = "master"

	digestField    = "digest"
	committedField = "committed"

	beginningOfTime = 0
	endOfTime       = math.MaxInt32

	// These parameters were chosen somewhat arbitrarily to make sure that if Firestore takes a while,
	// we try our best (but not too hard) to make reads or writes happen.
	maxOperationTime = 2 * time.Minute
	maxRetries       = 3

	// The number of shards was determined experimentally based on 500k and then 100k expectation
	// entries. 100k is about what we expect on a master partition. 100 is a typical guess for any
	// particular CL expectation, so we can get away with fewer shards.
	masterPartitionShards = 16
	clPartitionShards     = 2
)

// Store implements expectations.Store backed by Firestore. It has a local expectationCache of the
// expectations to reduce load on firestore
type Store struct {
	client *ifirestore.Client
	mode   AccessMode

	// Sharding our loading of expectations can drastically improve throughput. The number of shards
	// can be different, depending on the approximate size of partition. We generally assume that
	// the number of expectations on the master partition is much much bigger than on any other
	// partition.
	numShards int

	// CL expectations are kept apart from those on master by dividing how we store expectations
	// into partitions. Partitions look like "master" or "gerrit_123456".
	partition string

	// notifier allows this Store to communicate with the outside world when
	// expectations change.
	notifier expectations.ChangeNotifier

	// entryCache is an in-memory representation of the expectations in Firestore.
	entryCache      map[expectations.ID]expectationEntry
	entryCacheMutex sync.RWMutex

	// returnCache allows us to cache the return value for Get() if there haven't been any changes
	// to the expectations since the previous call.
	returnCache *expectations.Expectations
	// this mutex is for the returnCacheObject (i.e. setting it to nil. expectations.Expectations
	// is thread-safe for setting/updating).
	returnCacheMutex sync.Mutex

	// hasSnapshotsRunning tracks if the snapshots were started due to a call from Initialize().
	hasSnapshotsRunning bool
	// now allows the mocking of time in tests.
	now func() time.Time
}

// AccessMode indicates if this ExpectationsStore can update existing Expectations
// in the backing store or if if can only read them.
type AccessMode int

const (
	ReadOnly AccessMode = iota
	ReadWrite
)

var (
	// ReadOnlyErr will be returned if any mutating actions are called on the Store and it is not
	// configured for that.
	ReadOnlyErr = errors.New("expectationStore is in read-only mode")
)

// expectationEntry is the document type stored in the expectationsCollection.
type expectationEntry struct {
	Grouping types.TestName `firestore:"grouping"`
	Digest   types.Digest   `firestore:"digest"`
	Updated  time.Time      `firestore:"updated"`
	LastUsed time.Time      `firestore:"last_used"`
	// This is sorted by FirstIndex and should have no duplicate ranges for FirstIndex and LastIndex.
	// That is, we should not have two ranges that both cover [3, 9].
	Ranges  []triageRange `firestore:"ranges"`
	NeedsGC bool          `firestore:"needs_gc"`
}

// ID returns the deterministic ID that lets us update existing entries.
func (e *expectationEntry) ID() string {
	s := string(e.Grouping) + "|" + string(e.Digest)
	// firestore gets cranky if there are / in key names
	return strings.Replace(s, "/", "-", -1)
}

// expectationChange represents the changing of a single expectation entry.
type expectationChange struct {
	// RecordID refers to a document in the records collection.
	RecordID      string             `firestore:"record_id"`
	Grouping      types.TestName     `firestore:"grouping"`
	Digest        types.Digest       `firestore:"digest"`
	AffectedRange triageRange        `firestore:"affected_range"`
	LabelBefore   expectations.Label `firestore:"label_before"`
}

type triageRange struct {
	FirstIndex int                `firestore:"first_index"`
	LastIndex  int                `firestore:"last_index"`
	Label      expectations.Label `firestore:"label"`
}

// triageRecord represents a group of changes made in a single triage action by a user.
type triageRecord struct {
	UserName  string    `firestore:"user"`
	TS        time.Time `firestore:"ts"`
	Changes   int       `firestore:"changes"`
	Committed bool      `firestore:"committed"`
}

func New(client *ifirestore.Client, cn expectations.ChangeNotifier, mode AccessMode) *Store {
	return &Store{
		client:     client,
		notifier:   cn,
		partition:  masterPartition,
		numShards:  masterPartitionShards,
		mode:       mode,
		entryCache: map[expectations.ID]expectationEntry{},
		now:        time.Now,
	}
}

// Initialize begins several goroutines which monitor Firestore QuerySnapshots, will begin
// watching for changes to the expectations, keeping the cache fresh. This also loads the initial
// set of expectation entries into the local RAM cache. It should be called on long-lived instances
// which need the in-memory cache to stay synchronized with the underlying Firestore collection.
func (s *Store) Initialize(ctx context.Context) error {
	// Make the initial query of all expectations currently in the store, sharded so as to improve
	// performance.
	queries := fs_utils.ShardOnDigest(s.expectationsCollection(), digestField, s.numShards)
	expectationSnapshots := make([]*firestore.QuerySnapshotIterator, s.numShards)
	entriesByShard := make([][]expectationEntry, s.numShards)
	var eg errgroup.Group
	for shard, q := range queries {
		func(shard int, q firestore.Query) {
			eg.Go(func() error {
				snap := q.Snapshots(ctx)
				qs, err := snap.Next()
				if err != nil {
					return skerr.Wrapf(err, "getting initial snapshot data shard[%d]", shard)
				}
				entriesByShard[shard] = extractExpectationEntries(qs)
				expectationSnapshots[shard] = snap
				return nil
			})
		}(shard, q)
	}
	err := eg.Wait()
	if err != nil {
		return skerr.Wrap(err)
	}

	// Now we load the RAM cache with all of the loaded expectations.
	s.entryCacheMutex.Lock()
	defer s.entryCacheMutex.Unlock()
	for _, entries := range entriesByShard {
		for _, e := range entries {
			// Due to how we shard our queries, there shouldn't be any overwriting of one shard
			// over another.
			id := expectations.ID{
				Grouping: e.Grouping,
				Digest:   e.Digest,
			}
			s.entryCache[id] = e
		}
	}

	// Re-using those shards from earlier, we start goroutines to wait for any new expectation
	// entries to be seen or changes to existing ones. When those happen, we update the cache.
	// TODO(kjlubick) add an alert for this metric if it becomes non-zero.
	stoppedShardsMetric := metrics2.GetCounter("stopped_expstore_shards")
	stoppedShardsMetric.Reset()
	for i := 0; i < s.numShards; i++ {
		go func(shard int) {
			snapFactory := func() *firestore.QuerySnapshotIterator {
				queries := fs_utils.ShardOnDigest(s.expectationsCollection(), digestField, s.numShards)
				return queries[shard].Snapshots(ctx)
			}
			// reuse the initial snapshots, so we don't have to re-load all the data again (for
			// which there could be a lot).
			err := fs_utils.ListenAndRecover(ctx, expectationSnapshots[shard], snapFactory, s.updateCacheAndNotify)
			if err != nil {
				sklog.Errorf("Unrecoverable error: %s", err)
			}
			stoppedShardsMetric.Inc(1)
		}(i)
	}
	s.hasSnapshotsRunning = true
	return nil
}

// updateCacheAndNotify updates the cached expectations with the given snapshot and then sends
// notifications to the notifier about the updates.
func (s *Store) updateCacheAndNotify(_ context.Context, qs *firestore.QuerySnapshot) error {
	entries := extractExpectationEntries(qs)
	var toNotify []expectations.ID
	s.entryCacheMutex.Lock()
	defer s.entryCacheMutex.Unlock()
	for _, newEntry := range entries {
		id := expectations.ID{
			Grouping: newEntry.Grouping,
			Digest:   newEntry.Digest,
		}
		existing, ok := s.entryCache[id]
		// We always update to the cached version.
		s.entryCache[id] = newEntry
		if ok {
			// We get notifications when UpdateLastUsed updates timestamps. These don't require an
			// event to be published since they do not affect the labels. The best way to check if
			// something material changed is to compare the Updated timestamp.
			if existing.Updated.Equal(newEntry.Updated) {
				continue
			}
		}
		toNotify = append(toNotify, id)
	}
	if len(toNotify) > 0 {
		// There were a non-zero amount of actual changes to the expectations. Purge the cached
		// version that we return from Get.
		func() {
			s.returnCacheMutex.Lock()
			defer s.returnCacheMutex.Unlock()
			s.returnCache = nil
		}()
	}

	if s.notifier != nil {
		for _, id := range toNotify {
			s.notifier.NotifyChange(id)
		}
	}
	return nil
}

// extractExpectationEntries retrieves all expectation entries from a given QuerySnapshot, logging
// any errors (which should be exceedingly rare).
func extractExpectationEntries(qs *firestore.QuerySnapshot) []expectationEntry {
	var entries []expectationEntry
	for _, dc := range qs.Changes {
		if dc.Kind == firestore.DocumentRemoved {
			// TODO(kjlubick): It would probably be good to return a slice of expectation.IDs that
			//   can get removed from the cache.
			continue
		}
		entry := expectationEntry{}
		if err := dc.Doc.DataTo(&entry); err != nil {
			id := dc.Doc.Ref.ID
			sklog.Errorf("corrupt data in firestore, could not unmarshal expectationEntry with id %s", id)
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// ForChangeList implements the ExpectationsStore interface.
func (s *Store) ForChangeList(id, crs string) expectations.Store {
	if id == "" || crs == "" {
		// These must both be specified
		return nil
	}
	return &Store{
		client:    s.client,
		numShards: clPartitionShards,
		notifier:  nil, // we do not need to notify when ChangeList expectations change.
		partition: crs + "_" + id,
		mode:      s.mode,
		now:       s.now,
	}
}

// AddChange implements the ExpectationsStore interface.
func (s *Store) AddChange(ctx context.Context, delta []expectations.Delta, userID string) error {
	defer metrics2.FuncTimer().Stop()
	if s.mode == ReadOnly {
		return ReadOnlyErr
	}
	// Create the entries that we want to write (using the previous values)
	now := s.now()
	// TODO(kjlubick) If we support ranges, these constants will need to be changed.
	entries, changes, err := s.makeEntriesAndChanges(ctx, now, delta, beginningOfTime, endOfTime)
	if err != nil {
		return skerr.Wrapf(err, "preparing %d entries before storing", len(delta))
	}

	// Nothing to add
	if len(entries) == 0 {
		return nil
	}

	// firestore can do up to 500 writes at once, we have 2 writes per entry, plus 1 triageRecord
	const batchSize = (ifirestore.MAX_TRANSACTION_DOCS / 2) - 1

	b := s.client.Batch()

	// First write the triage record, with Committed being false (i.e. in progress)
	tr := s.recordsCollection().NewDoc()
	record := triageRecord{
		UserName:  userID,
		TS:        now,
		Changes:   len(entries),
		Committed: false,
	}
	b.Set(tr, record)
	err = s.client.BatchWrite(ctx, len(entries), batchSize, maxOperationTime, b, func(b *firestore.WriteBatch, i int) error {
		entry := entries[i]
		e := s.expectationsCollection().Doc(entry.ID())
		b.Set(e, entry)

		tc := s.changesCollection().NewDoc()
		change := changes[i]
		change.RecordID = tr.ID
		b.Set(tc, change)
		return nil
	})
	if err != nil {
		// We really hope this doesn't fail, because it could lead to a large batch triage that
		// is partially applied.
		return skerr.Wrap(err)
	}

	// We have succeeded this potentially long write, so mark it completed.
	update := map[string]interface{}{
		committedField: true,
	}
	_, err = s.client.Set(ctx, tr, update, 10, maxOperationTime, firestore.MergeAll)
	return err
}

func (s *Store) makeEntriesAndChanges(ctx context.Context, now time.Time, delta []expectations.Delta, firstIdx, lastIdx int) ([]expectationEntry, []expectationChange, error) {
	if err := s.updateEntryCacheIfNeeded(ctx); err != nil {
		return nil, nil, skerr.Wrap(err)
	}
	s.entryCacheMutex.RLock()
	defer s.entryCacheMutex.RUnlock()
	var entries []expectationEntry
	var changes []expectationChange

	for _, d := range delta {
		// Fetch what was there before - mainly to get the previous Ranges
		entry := s.entryCache[expectations.ID{
			Grouping: d.Grouping,
			Digest:   d.Digest,
		}]
		// These do nothing if the entry was already cached.
		entry.Grouping = d.Grouping
		entry.Digest = d.Digest
		// We intentionally do not set LastUsed here. It will be the zero value for time when
		// created, and we don't want to update LastUsed simply by triaging it.

		// Update Updated and Ranges with the new data.
		entry.Updated = now
		newRange := triageRange{
			FirstIndex: firstIdx,
			LastIndex:  lastIdx,
			Label:      d.Label,
		}
		previousLabel := expectations.Untriaged
		replacedRange := false
		// TODO(kjlubick): if needed, this could be a binary search, but since there will be < 20
		//   ranges for almost all entries, it probably doesn't matter.
		for i, r := range entry.Ranges {
			if r.FirstIndex == firstIdx && r.LastIndex == lastIdx {
				replacedRange = true
				previousLabel = r.Label
				entry.Ranges[i] = newRange
				break
			}
		}
		if !replacedRange {
			entry.Ranges = append(entry.Ranges, newRange)
			sort.Slice(entry.Ranges, func(i, j int) bool {
				return entry.Ranges[i].FirstIndex < entry.Ranges[j].FirstIndex
			})
		}

		entries = append(entries, entry)
		changes = append(changes, expectationChange{
			// RecordID will be filled out later
			Grouping:      d.Grouping,
			Digest:        d.Digest,
			AffectedRange: newRange,
			LabelBefore:   previousLabel,
		})
	}
	return entries, changes, nil
}

func (s *Store) updateEntryCacheIfNeeded(ctx context.Context) error {
	if s.hasSnapshotsRunning {
		return nil
	}
	// loadExpectations has the side-effect of updating s.entryCache, which is required if snapshots
	// are not running (which normally update the cache).
	_, err := s.loadExpectations(ctx)
	return skerr.Wrap(err)
}

// Get implements the expectations.Store interface. If the RAM cache of expectation entries has been
// created, Get may return a cached value when no changes have happened since the previous call to
// Get. Otherwise, a new return value will be created, from the RAM cache if available, or from
// Firestore if not.
func (s *Store) Get(ctx context.Context) (expectations.ReadOnly, error) {
	if s.hasSnapshotsRunning {
		// If the snapshots are running, we first check to see if we have a fresh Expectations.
		s.returnCacheMutex.Lock()
		defer s.returnCacheMutex.Unlock()
		if s.returnCache != nil {
			return s.returnCache, nil
		}
		// At this point, we do not have a fresh expectation.Expectations (something has changed
		// since the last time we made it), so we assemble a new one from our in-RAM cache of
		// expectation entries (which we assume to be up to date courtesy of our running snapshots).
		e := s.assembleExpectations()
		s.returnCache = e
		return e, nil
	}
	// If the snapshots are not loaded, we assume we do not have a RAM cache and load the
	// expectations from Firestore.
	return s.loadExpectations(ctx)
}

// GetCopy implements the expectations.Store interface.
func (s *Store) GetCopy(ctx context.Context) (*expectations.Expectations, error) {
	if s.hasSnapshotsRunning {
		// If the snapshots are running, we first check to see if we have a fresh Expectations.
		s.returnCacheMutex.Lock()
		defer s.returnCacheMutex.Unlock()
		if s.returnCache != nil {
			// The cache is fresh, so return a copy (so clients can mutate it if they need to).
			return s.returnCache.DeepCopy(), nil
		}
		// At this point, we do not have a fresh expectation.Expectations (something has changed
		// since the last time we made it), so we assemble a new one from our in-RAM cache of
		// expectation entries (which we assume to be up to date courtesy of our running snapshots).
		return s.assembleExpectations(), nil

	}
	// If the snapshots are not loaded, we assume we do not have a RAM cache and load the
	// expectations from Firestore.
	return s.loadExpectations(ctx)
}

// loadExpectations fetches the expectations from Firestore and returns them. It shards the query
// to expedite the process. The fetched expectationEntry will be cached in the s.entryCache, under
// the assumption that loadExpectations will only be called for setups that do not have the
// snapshot queries, and the entryCache is used to create the expectationChanges (for undoing).
func (s *Store) loadExpectations(ctx context.Context) (*expectations.Expectations, error) {
	defer metrics2.FuncTimer().Stop()
	es := make([][]expectationEntry, s.numShards)
	queries := fs_utils.ShardOnDigest(s.expectationsCollection(), digestField, s.numShards)

	err := s.client.IterDocsInParallel(ctx, "loadExpectations", s.partition, queries, maxRetries, maxOperationTime, func(i int, doc *firestore.DocumentSnapshot) error {
		if doc == nil {
			return nil
		}
		entry := expectationEntry{}
		if err := doc.DataTo(&entry); err != nil {
			id := doc.Ref.ID
			return skerr.Wrapf(err, "corrupt data in firestore, could not unmarshal expectationEntry with id %s", id)
		}
		if len(entry.Ranges) == 0 {
			// This should never happen, but we'll ignore these malformed entries if they do.
			return nil
		}
		es[i] = append(es[i], entry)
		return nil
	})

	if err != nil {
		return nil, skerr.Wrapf(err, "fetching expectations for partition %s", s.partition)
	}

	e := expectations.Expectations{}
	toCache := map[expectations.ID]expectationEntry{}
	for _, entries := range es {
		for _, entry := range entries {
			// TODO(kjlubick) If we decide to handle ranges of expectations, Get will need to take a
			//   parameter indicating the commit index for which we should return valid ranges.
			e.Set(entry.Grouping, entry.Digest, entry.Ranges[0].Label)
			toCache[expectations.ID{
				Grouping: entry.Grouping,
				Digest:   entry.Digest,
			}] = entry
		}
	}
	s.entryCacheMutex.Lock()
	defer s.entryCacheMutex.Unlock()
	s.entryCache = toCache
	return &e, nil
}

// assembleExpectations creates an Expectations from the entryCache. It will copy any data it needs,
// so the return value can be mutated freely.
func (s *Store) assembleExpectations() *expectations.Expectations {
	s.entryCacheMutex.RLock()
	defer s.entryCacheMutex.RUnlock()

	e := &expectations.Expectations{}
	for id, entry := range s.entryCache {
		if len(entry.Ranges) == 0 {
			sklog.Warningf("ignoring invalid entry for id %s", id)
			continue
		}
		// TODO(kjlubick) If we decide to handle ranges of expectations, Get will need to take a
		//   parameter indicating the commit index for which we should return valid ranges.
		e.Set(entry.Grouping, entry.Digest, entry.Ranges[0].Label)
	}
	return e
}

// QueryLog implements the expectations.Store interface
func (s *Store) QueryLog(ctx context.Context, offset, size int, details bool) ([]expectations.TriageLogEntry, int, error) {
	panic("todo")
}

// UndoChange implements the expectations.Store interface.
func (s *Store) UndoChange(ctx context.Context, changeID, userID string) error {
	panic("todo")
}

// GetTriageHistory implements the expectations.Store interface.
func (s *Store) GetTriageHistory(ctx context.Context, grouping types.TestName, digest types.Digest) ([]expectations.TriageHistory, error) {
	panic("todo")
}

// UpdateLastUsed implements the expectations.GarbageCollector interface.
func (s *Store) UpdateLastUsed(ctx context.Context, ids []expectations.ID, now time.Time) error {
	panic("todo")
}

// MarkUnusedEntriesForGC implements the expectations.GarbageCollector interface.
func (s *Store) MarkUnusedEntriesForGC(ctx context.Context, label expectations.Label, ts time.Time) (int, error) {
	panic("todo")
}

// GarbageCollect implements the expectations.GarbageCollector interface.
func (s *Store) GarbageCollect(ctx context.Context) (int, error) {
	panic("todo")
}

func (s *Store) expectationsCollection() *firestore.CollectionRef {
	return s.client.Collection(partitions).Doc(s.partition).Collection(expectationEntries)
}

func (s *Store) recordsCollection() *firestore.CollectionRef {
	return s.client.Collection(partitions).Doc(s.partition).Collection(recordEntries)
}

func (s *Store) changesCollection() *firestore.CollectionRef {
	return s.client.Collection(partitions).Doc(s.partition).Collection(changeEntries)
}

// Make sure Store fulfills the expectations.Store interface
var _ expectations.Store = (*Store)(nil)

// Make sure Store fulfills the expectations.GarbageCollector interface
var _ expectations.GarbageCollector = (*Store)(nil)
