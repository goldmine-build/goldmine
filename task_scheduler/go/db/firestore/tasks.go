package firestore

import (
	"context"
	"fmt"
	"sort"
	"time"

	fs "cloud.google.com/go/firestore"
	"go.skia.org/infra/go/firestore"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/task_scheduler/go/db"
	"go.skia.org/infra/task_scheduler/go/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	COLLECTION_TASKS = "tasks"
)

// Fix all timestamps for the given task.
func fixTaskTimestamps(task *types.Task) {
	task.Created = firestore.FixTimestamp(task.Created)
	task.DbModified = firestore.FixTimestamp(task.DbModified)
	task.Finished = firestore.FixTimestamp(task.Finished)
	task.Started = firestore.FixTimestamp(task.Started)
}

// tasks returns a reference to the tasks collection.
func (d *firestoreDB) tasks() *fs.CollectionRef {
	return d.client.Collection(COLLECTION_TASKS)
}

// See documentation for types.TaskReader interface.
func (d *firestoreDB) GetTaskById(id string) (*types.Task, error) {
	doc, err := d.client.Get(context.TODO(), d.tasks().Doc(id), DEFAULT_ATTEMPTS, GET_SINGLE_TIMEOUT)
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var rv types.Task
	if err := doc.DataTo(&rv); err != nil {
		return nil, err
	}
	return &rv, nil
}

// See documentation for types.TaskReader interface.
func (d *firestoreDB) GetTasksFromDateRange(start, end time.Time, repo string) ([]*types.Task, error) {
	var tasks [][]*types.Task
	init := func(numGoroutines int) {
		tasks = make([][]*types.Task, numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			estResults := estResultSize(end.Sub(start) / time.Duration(numGoroutines))
			tasks[i] = make([]*types.Task, 0, estResults)
		}
	}
	elem := func(idx int, doc *fs.DocumentSnapshot) error {
		var task types.Task
		if err := doc.DataTo(&task); err != nil {
			return err
		}
		if doc.Ref.ID != task.Id {
			sklog.Errorf("Task %s is stored with ID %s; GetTaskById will not be able to find it!", task.Id, doc.Ref.ID)
			return nil
		}
		if repo != "" {
			if task.Repo != repo {
				sklog.Errorf("Query returned task with wrong repo; wanted %q but got %q; task: %+v", repo, task.Repo, task)
				return nil
			}
		}
		tasks[idx] = append(tasks[idx], &task)
		return nil
	}
	q := d.tasks().Query
	if repo != "" {
		q = q.Where(KEY_REPO, "==", repo)
	}
	if err := d.dateRangeHelper("GetTasksFromDateRange", q, start, end, init, elem); err != nil {
		return nil, err
	}
	totalResults := 0
	for _, taskList := range tasks {
		totalResults += len(taskList)
	}
	rv := make([]*types.Task, 0, totalResults)
	for _, taskList := range tasks {
		rv = append(rv, taskList...)
	}
	sort.Sort(types.TaskSlice(rv))
	return rv, nil
}

// See documentation for types.TaskDB interface.
func (d *firestoreDB) AssignId(task *types.Task) error {
	task.Id = firestore.AlphaNumID()
	return nil
}

// putTasks sets the contents of the given tasks in Firestore, as part of the
// given transaction. It is used by PutTask and PutTasks.
func (d *firestoreDB) putTasks(tasks []*types.Task, isNew []bool, prevModified []time.Time, tx *fs.Transaction) error {
	// Find the previous versions of the tasks. Ensure that they weren't
	// updated concurrently.
	refs := make([]*fs.DocumentRef, 0, len(tasks))
	for _, task := range tasks {
		refs = append(refs, d.tasks().Doc(task.Id))
	}
	docs, err := tx.GetAll(refs)
	if err != nil {
		return err
	}
	d.client.CountReadQueryAndRows(d.tasks().Path, len(docs))
	for idx, doc := range docs {
		if !doc.Exists() {
			// This is expected for new tasks.
			if !isNew[idx] {
				sklog.Errorf("Task is not new but wasn't found in the DB: %+v", tasks[idx])
				// If the task is supposed to exist but does not, then
				// we have a problem.
				return db.ErrConcurrentUpdate
			}
		} else if isNew[idx] {
			// If the task is not supposed to exist but does, then
			// we have a problem.
			var old types.Task
			if err := doc.DataTo(&old); err != nil {
				return fmt.Errorf("Task has no DbModified timestamp but already exists in the DB. Failed to decode previous task with: %s", err)
			}
			sklog.Errorf("Task has no DbModified timestamp but already exists in the DB! \"New\" task:\n%+v\nExisting task:\n%+v", tasks[idx], old)
			return db.ErrConcurrentUpdate
		}
		// If the task already exists, check the DbModified timestamp
		// to ensure that someone else didn't update it.
		if !isNew[idx] {
			var old types.Task
			if err := doc.DataTo(&old); err != nil {
				return err
			}
			if old.DbModified != prevModified[idx] {
				return db.ErrConcurrentUpdate
			}
		}
	}

	// Set the new contents of the tasks.
	d.client.CountWriteQueryAndRows(d.tasks().Path, len(tasks))
	for _, task := range tasks {
		ref := d.tasks().Doc(task.Id)
		if err := tx.Set(ref, task); err != nil {
			return err
		}
	}
	return nil
}

// See documentation for types.TaskDB interface.
func (d *firestoreDB) PutTask(task *types.Task) error {
	return d.PutTasks([]*types.Task{task})
}

// See documentation for types.TaskDB interface.
func (d *firestoreDB) PutTasks(tasks []*types.Task) (rvErr error) {
	if len(tasks) > MAX_TRANSACTION_DOCS {
		return fmt.Errorf("Tried to insert %d tasks but Firestore maximum per transaction is %d.", len(tasks), MAX_TRANSACTION_DOCS)
	}

	// Record the previous ID and DbModified timestamp. We'll reset these
	// if we fail to insert the tasks into the DB.
	now := firestore.FixTimestamp(time.Now())
	isNew := make([]bool, len(tasks))
	prevId := make([]string, len(tasks))
	prevModified := make([]time.Time, len(tasks))
	for idx, task := range tasks {
		if util.TimeIsZero(task.Created) {
			return fmt.Errorf("Created not set. Task %s created time is %s. %v", task.Id, task.Created, task)
		}
		isNew[idx] = util.TimeIsZero(task.DbModified)
		prevId[idx] = task.Id
		prevModified[idx] = task.DbModified
	}
	defer func() {
		if rvErr != nil {
			for idx, task := range tasks {
				task.Id = prevId[idx]
				task.DbModified = prevModified[idx]
			}
		}
	}()

	// Assign new IDs (where needed) and DbModified timestamps.
	for _, task := range tasks {
		if task.Id == "" {
			if err := d.AssignId(task); err != nil {
				return err
			}
		}
		if !now.After(task.DbModified) {
			// We can't use the same DbModified timestamp for two updates,
			// or we risk losing updates. Increment the timestamp if
			// necessary.
			task.DbModified = task.DbModified.Add(firestore.TS_RESOLUTION)
		} else {
			task.DbModified = now
		}
		fixTaskTimestamps(task)
	}

	// Insert the tasks into the DB.
	if err := d.client.RunTransaction(context.TODO(), "PutTasks", fmt.Sprintf("%d tasks", len(tasks)), DEFAULT_ATTEMPTS, PUT_MULTI_TIMEOUT, func(ctx context.Context, tx *fs.Transaction) error {
		return d.putTasks(tasks, isNew, prevModified, tx)
	}); err != nil {
		return err
	}
	return nil
}

// See documentation for types.TaskDB interface.
func (d *firestoreDB) PutTasksInChunks(tasks []*types.Task) error {
	return util.ChunkIter(len(tasks), MAX_TRANSACTION_DOCS, func(i, j int) error {
		return d.PutTasks(tasks[i:j])
	})
}
