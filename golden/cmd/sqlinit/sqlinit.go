// The sqlinit executable creates a database on the production SQL cluster with the appropriate
// schema. It will not modify any tables (e.g. add missing indexes or change columns).
// This executable will schedule new automatic backups, so if there are existing ones, one may have
// to drop the old schedules.
// https://www.cockroachlabs.com/docs/v20.2/show-schedules
// https://www.cockroachlabs.com/docs/v20.2/drop-schedules
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"text/template"
	"time"

	"go.goldmine.build/go/sklog/sklogimpl"
	"go.goldmine.build/go/sklog/stdlogging"

	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/go/sql/schema"
)

func execSql(dbURL, sqlCmd string) {
	out, err := exec.Command(
		"cockroach", "sql",
		"--insecure", "--url="+dbURL,
		"--execute="+sqlCmd,
	).CombinedOutput()
	if err != nil {
		sklog.Fatalf("%s: %s: %s: %v", dbURL, sqlCmd, string(out), err)
	}
	sklog.Infof("Out: %s", string(out))
}

func main() {
	// Why aren't these read from the config?
	backupBucket := flag.String("backup_bucket", "goldmine-gold-database-backups", "The bucket backups should be written to. Defaults to public bucket.")
	dbURL := flag.String("db_cluster", "postgres://root@goldmine-prime:26257/gold", "The URL of the cluster")
	dbName := flag.String("db_name", "gold", "name of database to init")

	sklogimpl.SetLogger(stdlogging.New(os.Stderr))
	flag.Parse()
	if *dbName == "" {
		sklog.Fatalf("Must supply db_name")
	}
	if *dbURL == "" {
		sklog.Fatalf("Must supply db_cluster")
	}
	if *backupBucket == "" {
		sklog.Fatalf("Must supply backup_bucket")
	}
	// Both k8s and cockroachdb expect database names to be lowercase.
	normalizedDB := strings.ToLower(*dbName)

	sklog.Infof("Creating database %s", normalizedDB)
	execSql(*dbURL, "CREATE DATABASE IF NOT EXISTS "+normalizedDB+";")

	sklog.Infof("Creating tables")
	execSql(*dbURL, schema.Schema)

	sklog.Infof("Deleting existing schedules, if any")
	execSql(*dbURL, dropExistingSchedules(normalizedDB))

	// Make sure the drop commands really finish before creating new things.
	time.Sleep(2 * time.Second)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	sklog.Infof("Creating automatic backup schedules")
	execSql(*dbURL, getSchedules(schema.Tables{}, *backupBucket, normalizedDB, rng))

	sklog.Info("Done")
}

func dropExistingSchedules(db string) string {
	// https://www.cockroachlabs.com/docs/stable/drop-schedules.html#drop-multiple-schedules
	// Note that we have to escape the underscore because in a LIKE query, underscore represents
	// any single character. We don't want skia_ to match skiainfra_weekly, only things like
	// skia_weekly.
	return `DROP SCHEDULES SELECT id FROM [SHOW SCHEDULES] WHERE label LIKE '` + db + `\_%';`
}

type backupCadence struct {
	// We accept "daily", "weekly", "monthly" and apply some jitter based on those to make valid
	// crontab formats for CockroachDB.
	cadence string
	tables  []string
}

type jitterSource interface {
	Intn(n int) int
}

// getSchedules returns SQL commands to create backups according to the sql_backup annotations
// on the provided type scoped to the given database name. It will group all like cadences together
// in one backup operation. It panics if any field is not a slice (i.e. not representing a row)
// or if any field is missing the sql_backup annotation. If we want a table to not be backed up,
// we must explicitly opt out by setting the cadence to "none". Jitter will be applied to the
// backups so all the daily backups don't happen at midnight, for example.
func getSchedules(inputType interface{}, gcsBucket, dbName string, rng jitterSource) string {
	var schedules []*backupCadence

	t := reflect.TypeOf(inputType)
	for i := 0; i < t.NumField(); i++ {
		table := t.Field(i) // Fields of the outer type are expected to be tables.
		if table.Type.Kind() != reflect.Slice {
			panic(`Expected table should be a slice: ` + table.Name)
		}
		cadence, ok := table.Tag.Lookup("sql_backup")
		if !ok {
			panic(`Expected table should have backup cadence. Did you mean "none"? ` + table.Name)
		}
		if cadence == "none" {
			continue
		}
		found := false
		for _, s := range schedules {
			if s.cadence == cadence {
				found = true
				s.tables = append(s.tables, dbName+"."+table.Name)
				break
			}
		}
		if found {
			continue
		}
		schedules = append(schedules, &backupCadence{
			cadence: cadence,
			tables:  []string{dbName + "." + table.Name},
		})
	}
	body := strings.Builder{}
	templ := template.Must(template.New("").Parse(scheduleTemplate))
	for _, s := range schedules {
		err := templ.Execute(&body, scheduleContext{
			Cadence:           s.cadence,
			CadenceWithJitter: applyJitter(s.cadence, rng),
			DBName:            dbName,
			GCSBucket:         gcsBucket,
			Tables:            strings.Join(s.tables, ", "),
		})
		if err != nil {
			panic(err)
		}
	}
	return body.String()
}

// applyJitter randomizes a given cadence slightly to avoid all backups happening at once.
// If there is an unknown cadence, this panics. It returns a crontab format string indicating when
// the backups should occur using the provided source of random ints.
func applyJitter(cadence string, rng jitterSource) string {
	m := rng.Intn(60)
	// These times will be in UTC. We would like backups to happen between 11pm and 4am Eastern
	// (given our current client locations). This is between 4am and 9am UTC. (a 1 hour shift during
	// daylight savings time is fine).
	h := rng.Intn(5) + 4
	switch cadence {
	case "daily":
		return fmt.Sprintf("%d %d * * *", m, h)
	case "weekly":
		// Weekly backups happen on Sunday
		return fmt.Sprintf("%d %d * * 0", m, h)
	case "monthly":
		// Monthly backups happen sometime within the first 28 days of the month starting at
		// 4am UTC with some minute jitter because these tables are big and could take a while.
		return fmt.Sprintf("%d 4 %d * *", m, rng.Intn(28)+1)
	default:
		panic("Unknown cadence " + cadence)
	}
}

type scheduleContext struct {
	Cadence           string
	CadenceWithJitter string
	DBName            string
	GCSBucket         string
	Tables            string
}

const scheduleTemplate = `CREATE SCHEDULE {{.DBName}}_{{.Cadence}}
FOR BACKUP TABLE {{.Tables}}
INTO 'gs://{{.GCSBucket}}/{{.DBName}}/{{.Cadence}}?AUTH=implicit'
  RECURRING '{{.CadenceWithJitter}}'
  FULL BACKUP ALWAYS WITH SCHEDULE OPTIONS ignore_existing_backups;
`
