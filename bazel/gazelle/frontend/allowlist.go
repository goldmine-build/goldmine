package frontend

// targetDirectories is the set of directories for which this Gazelle extension will generate or
// update BUILD files.
//
// The value of this map indicates whether to recurse into the directory.
//
// TODO(lovisolo): Delete once we are targeting the entire repository.
var targetDirectories = map[string]bool{
	"infra-sk/modules":    true,
	"machine/modules":     true,
	"machine/pages":       true,
	"new_element/modules": true,
	"puppeteer-tests":     true,
	"task_driver/modules": true,
}
