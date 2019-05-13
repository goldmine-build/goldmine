package manual

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/deepequal"
	"go.skia.org/infra/go/firestore"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/go/util"
)

const (
	rollerName = "my-roller"
)

// req returns a dummy ManualRollRequest.
func req() *ManualRollRequest {
	return &ManualRollRequest{
		Requester:  "user@google.com",
		Result:     RESULT_FAILURE,
		Revision:   "abc123",
		RollerName: rollerName,
		Status:     STATUS_COMPLETE,
		Timestamp:  firestore.FixTimestamp(time.Now()),
		Url:        "http://my-roll.com",
	}
}

func TestCopyManualRollRequest(t *testing.T) {
	unittest.SmallTest(t)
	v := req()
	v.Id = "abc123"
	v.DbModified = time.Now()
	deepequal.AssertCopy(t, v, v.Copy())
}

func TestRequestValidation(t *testing.T) {
	unittest.SmallTest(t)

	check := func(r *ManualRollRequest, expectErr string) {
		err := r.Validate()
		if expectErr != "" {
			assert.EqualError(t, err, expectErr)
		} else {
			assert.NoError(t, err)
		}
	}

	// The base ManualRollRequest should be valid.
	r := req()
	check(r, "")

	// These properties are always required.
	r.Requester = ""
	check(r, "Requester is required.")
	r.Requester = "user@google.com"

	r.Revision = ""
	check(r, "Revision is required.")
	r.Revision = "abc123"

	r.RollerName = ""
	check(r, "RollerName is required.")
	r.RollerName = "my-roller"

	r.Timestamp = time.Time{}
	check(r, "Timestamp is required.")
	r.Timestamp = time.Unix(0, 0)
	check(r, "Timestamp is required.")
	r.Timestamp = firestore.FixTimestamp(time.Now()).Add(time.Nanosecond)
	check(r, "Timestamp must be in UTC and truncated to microsecond precision.")
	r.Timestamp = firestore.FixTimestamp(r.Timestamp)
	check(r, "")

	r.Status = ""
	check(r, "Invalid status.")
	r.Status = "bogus"
	check(r, "Invalid status.")
	r.Status = STATUS_COMPLETE

	r.Result = "bogus"
	check(r, "Invalid result.")
	r.Result = RESULT_FAILURE

	// Pending requests have no result or URL.
	r.Result = RESULT_UNKNOWN
	r.Status = STATUS_PENDING
	r.Url = ""
	check(r, "")
	r.Result = RESULT_SUCCESS
	check(r, "Result is invalid for pending requests.")
	r.Result = RESULT_FAILURE
	check(r, "Result is invalid for pending requests.")
	r.Result = "bogus"
	check(r, "Invalid result.")
	r.Result = RESULT_UNKNOWN
	r.Url = "bogus"
	check(r, "Url is invalid for pending requests.")

	// Running requests have no result but do have a URL.
	r.Status = STATUS_STARTED
	r.Url = "http://my-roll.com"
	assert.NoError(t, r.Validate())
	check(r, "")
	r.Result = RESULT_SUCCESS
	check(r, "Result is invalid for running requests.")
	r.Result = RESULT_FAILURE
	check(r, "Result is invalid for running requests.")
	r.Result = RESULT_UNKNOWN
	r.Url = ""
	check(r, "Url is required for non-pending requests.")
	r.Url = "http://my-request.com"

	// Id and DbModified must be set together.
	r.Id = ""
	r.DbModified = firestore.FixTimestamp(time.Now())
	check(r, "Request has no ID but has non-zero DbModified timestamp.")
	r.DbModified = time.Time{}
	check(r, "")
	r.Id = "abc123"
	check(r, "Request has an ID but has a zero DbModified timestamp.")
	r.DbModified = time.Now()
}

func testDB(t *testing.T, db DB) {
	// No error for unknown roller.
	reqs, err := db.GetRecent(rollerName, 10)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(reqs))

	// Verify that we can't insert an invalid request.
	r := req()
	r.Id = ""
	r.RollerName = ""
	assert.EqualError(t, db.Put(r), "RollerName is required.")

	// Verify that we can't update a request which doesn't already exist.
	r.RollerName = rollerName
	r.Id = "bogus"
	r.DbModified = firestore.FixTimestamp(time.Now())
	assert.EqualError(t, db.Put(r), ErrNotFound.Error())

	// Verify that we can't insert a new request which has a non-zero
	// DbModified timestamp.
	r.Id = ""
	assert.EqualError(t, db.Put(r), "Request has no ID but has non-zero DbModified timestamp.")

	// Put and retrieve several requests.
	now := firestore.FixTimestamp(time.Now())
	for rev := 0; rev < 10; rev++ {
		r.Id = ""
		r.DbModified = time.Time{}
		r.Result = RESULT_UNKNOWN
		r.Revision = strconv.Itoa(rev)
		r.Status = STATUS_STARTED
		r.Timestamp = now.Add(time.Duration(rev) * time.Minute)
		assert.NoError(t, db.Put(r))
		assert.NotEqual(t, "", r.Id)
		assert.False(t, util.TimeIsZero(r.DbModified))
	}
	reqs, err = db.GetRecent(rollerName, 5)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(reqs))
	for idx, r := range reqs {
		rev, err := strconv.Atoi(r.Revision)
		assert.NoError(t, err)
		assert.Equal(t, 9-idx, rev)
	}

	// Verify that we can't insert an existing request which has a zero
	// DbModified timestamp.
	oldDbModified := reqs[0].DbModified
	reqs[0].DbModified = time.Time{}
	assert.EqualError(t, db.Put(reqs[0]), "Request has an ID but has a zero DbModified timestamp.")
	reqs[0].DbModified = oldDbModified

	// Retrieve the unfinished requests.
	inc, err := db.GetIncomplete(rollerName)
	assert.NoError(t, err)
	assert.Equal(t, 10, len(inc))

	// Update a request to indicate that it finished.
	reqs[3].Result = RESULT_SUCCESS
	reqs[3].Status = STATUS_COMPLETE
	id := reqs[3].Id
	assert.NoError(t, db.Put(reqs[3]))
	reqs, err = db.GetIncomplete(rollerName)
	assert.NoError(t, err)
	assert.Equal(t, 9, len(reqs))
	for _, req := range reqs {
		assert.NotEqual(t, id, req.Id)
	}
	reqs, err = db.GetRecent(rollerName, 10)
	assert.NoError(t, err)
	assert.Equal(t, 10, len(reqs))
	assert.Equal(t, id, reqs[3].Id)
	assert.Equal(t, RESULT_SUCCESS, reqs[3].Result)
	assert.Equal(t, STATUS_COMPLETE, reqs[3].Status)

	// Test concurrent update.
	reqs[0].DbModified = now.Add(-10 * time.Minute)
	oldDbModified = reqs[0].DbModified
	assert.EqualError(t, db.Put(reqs[0]), ErrConcurrentUpdate.Error())
	assert.Equal(t, reqs[0].DbModified, oldDbModified) // Verify that we didn't update DbModified.
}

func TestMemoryDB(t *testing.T) {
	unittest.SmallTest(t)
	db := NewInMemoryDB()
	defer util.Close(db)
	testDB(t, db)
}

func TestFirestoreDB(t *testing.T) {
	unittest.ManualTest(t)
	instance := fmt.Sprintf("test-%s", uuid.New())
	db, err := NewDB(context.Background(), firestore.FIRESTORE_PROJECT, instance, nil)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, db.(*firestoreDB).client.RecursiveDelete(db.(*firestoreDB).client.ParentDoc, 5, 30*time.Second))
		assert.NoError(t, db.Close())
	}()
	testDB(t, db)
}
