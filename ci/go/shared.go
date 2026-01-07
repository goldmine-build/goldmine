package shared

import "fmt"

const GitHubGoldMineCIQueue = "GITHUB_GOLDMINE_CI_QUEUE"

// CIWorkflowArgs is all the info we need to send off to Restate to run the CI.
type CIWorkflowArgs struct {
	PRNumber int    `json:"pr"`
	SHA      string `json:"sha"`
	Login    string `json:"login"`
}

func (c *CIWorkflowArgs) IdempotencyKey() string {
	if c.PRNumber != 0 {
		return fmt.Sprintf("PR-%d-%s", c.PRNumber, c.SHA)
	}
	return fmt.Sprintf("COMMIT-%s", c.SHA)
}
