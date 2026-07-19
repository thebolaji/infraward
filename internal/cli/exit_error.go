package cli

// ExitError carries the process exit code a subcommand wants, distinct from
// whether it also has an error message to print. Exit codes are 0 clean,
// 1 findings, 2 error - so "there were findings" must be distinguishable
// from "something went wrong" even though both are non-nil errors to cobra.
type ExitError struct {
	Code int
	Err  error // nil for a clean "findings" exit (code 1) with no error to print
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}
