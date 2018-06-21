package expstorage

import (
	"cloud.google.com/go/datastore"

	"go.skia.org/infra/golden/go/types"
)

const (
	// masterIssueID is the value used for IssueID when we dealing with the
	// master branch. Any IssueID < 0 should be ignored.
	masterIssueID = -1
)

// ExpChange is used to store an expectation change in the database. Each
// expectation change is an atomic change to expectations for an issue.
// The actual expectations are captured in instances of TestDigestExp.
type ExpChange struct {
	ChangeID         *datastore.Key `datastore:"__key__"`
	IssueID          int64
	UserID           string
	TimeStamp        int64 `datastore:",noindex"`
	Count            int64 `datastore:",noindex"`
	UndoChangeID     int64
	OK               bool
	ExpectationsBlob *datastore.Key `datastore:",noindex"`
}

// EventExpectationChange is the structure that is sent in expectation change events.
// When the change happened on the master branch 'IssueID' will contain a value <0
// and should be ignored.
type EventExpectationChange struct {
	IssueID     int64
	TestChanges map[string]types.TestClassification
}

// evExpChange creates a new instance of EventExptationChange.
func evExpChange(changes map[string]types.TestClassification, issueID int64) *EventExpectationChange {
	return &EventExpectationChange{
		TestChanges: changes,
		IssueID:     issueID,
	}
}

// expectationsState stores the state of expecations for either master or a Gerrit issue.
type expectationsState struct {
	ExpectationsBlob *datastore.Key // key of the blob that stores expectations
}
