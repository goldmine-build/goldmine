package shared

const GitHubGoldMineCIQueue = "GITHUB_GOLDMINE_CI_QUEUE"

// TrybotWorkflowArgs is all the info we need to send off to Temporal to run the
// CI.
type TrybotWorkflowArgs struct {
	PRNumber       int    `json:"pr"`
	PatchsetNumber int    `json:"patchset"`
	SHA            string `json:"sha"`
	Login          string `json:"login"`
}
