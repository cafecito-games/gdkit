// Package failure carries the machine-readable kind of a gdkit failure from the
// package that detected it to the command that reports it.
//
// Every exit-2 path writes a human-readable message. Under `--format json` the
// command also needs a stable identifier for what went wrong, and only the
// package that detected the failure knows which one applies: a configuration
// file can fail to be read, to parse, to name only known keys, to satisfy the
// version floor, or to validate, and a consumer reacts differently to each.
package failure

import "errors"

// Kind values are a public contract. They appear in JSON error output, so a
// consumer branches on them and renaming one breaks that consumer, exactly as
// renaming a lint rule breaks a project's configuration.
const (
	// ConfigRead is a configuration file that could not be read.
	ConfigRead = "config.read"
	// ConfigParse is a configuration file that is not well-formed JSON, or
	// that holds more than one JSON value.
	ConfigParse = "config.parse"
	// ConfigUnknownKey is a configuration key the schema does not define. The
	// error carries the key's full JSON path.
	ConfigUnknownKey = "config.unknown_key"
	// ConfigInvalid is a configuration whose values do not validate.
	ConfigInvalid = "config.invalid"
	// ConfigVersionFloor is a configuration whose minimum_gdkit_version the
	// running binary does not satisfy.
	ConfigVersionFloor = "config.version_floor"

	// UsageArguments is a wrong number of positional arguments.
	UsageArguments = "usage.arguments"
	// UsageFormat is an unknown --format value.
	UsageFormat = "usage.format"
	// UsageVersionFloor is a --minimum-version the running binary does not
	// satisfy, or that is not a major.minor.patch version.
	UsageVersionFloor = "usage.version_floor"

	// ProjectLoad is a failure walking or reading the project.
	ProjectLoad = "project.load"
	// AnalysisFailed is a failure during analysis itself.
	AnalysisFailed = "analysis.failed"
	// OutputWrite is a failure writing the report.
	OutputWrite = "output.write"
	// FileWrite is a failure writing a file a command was asked to create.
	FileWrite = "file.write"
)

// Error is an error that knows its own kind.
type Error struct {
	// Kind is one of the constants above.
	Kind string
	// Path is the file the failure is about, when there is one.
	Path string
	// Key is the configuration key the failure is about, when there is one.
	Key string

	Err error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Wrap labels err with a kind. It returns nil when err is nil so a caller can
// wrap a result directly.
func Wrap(kind string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Err: err}
}

// WrapPath labels err with a kind and the file it is about.
func WrapPath(kind, path string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Path: path, Err: err}
}

// WrapKey labels err with a kind and the configuration key it is about.
func WrapKey(kind, path, key string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Path: path, Key: key, Err: err}
}

// Of returns the labelled error in err's chain, if any. A caller that finds
// none falls back to the kind that fits its own call site, so every failure
// reports a kind even where the detecting package does not label one yet.
func Of(err error) (*Error, bool) {
	var labelled *Error
	if errors.As(err, &labelled) {
		return labelled, true
	}
	return nil, false
}
