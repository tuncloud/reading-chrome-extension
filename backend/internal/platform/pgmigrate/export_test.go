package pgmigrate

// Files, Plan and UpThrough expose the embedded migrations, the version planner
// and a bounded Up to the tests.
var (
	Files     = files
	Plan      = plan
	UpThrough = up
)
