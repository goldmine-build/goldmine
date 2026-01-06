package shared

const GitHubGoldMineCIQueue = "GITHUB_GOLDMINE_CI_QUEUE"

// CIWorkflowArgs is all the info we need to send off to Restate to run the CI.
type CIWorkflowArgs struct {
	PRNumber int    `json:"pr"`
	SHA      string `json:"sha"`
	Login    string `json:"login"`
}
