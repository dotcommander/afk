package commands

// ExitError carries an exit code from a cobra RunE handler up through
// root.Execute() so main can translate it into os.Exit(Code). cobra
// otherwise collapses every non-nil error to exit 1; commands that need
// a distinct code (e.g. afk import: 1 validation, 2 cycle, 3 duplicate)
// return *ExitError and let main do the os.Exit.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }
