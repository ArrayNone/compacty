package main

type ExitCode int

const (
	// 1 = Other errors, typically errors that occurs during compression
	BadUsage = 2 // Incorrect usage
	BadInput = 3 // No valid inputs

	BadConfig            = 10 // Invaild configuration
	CannotRetrieveConfig = 11 // Failed to create or get the config file

	Interrupted = 130 // User interrupts the tool by Ctrl+C
)

type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	return e.Err.Error()
}
