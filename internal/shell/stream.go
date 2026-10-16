package shell

import (
	"context"
	"os"
)

// RunAndCaptureStream executes a shell command and streams output chunks
// to onProgress as they arrive. The returned output is bounded: a command
// that prints more than [maxCaptureRetain] yields its head and tail rather
// than the whole stream, so a runaway command cannot cost its output size
// in memory.
func RunAndCaptureStream(ctx context.Context, opts RunOptions, onProgress func(string)) (CaptureResult, error) {
	if opts.Env == nil {
		opts.Env = os.Environ()
	}
	opts.Env = append(opts.Env, ptyColorEnvVars...)

	buf := newBoundedCapture(onProgress)
	opts.Stdout = buf
	opts.Stderr = buf

	runErr := Run(ctx, opts)

	exitCode := 0
	if runErr != nil {
		exitCode = ExitCode(runErr)
	}

	return CaptureResult{
		Output:   buf.String(),
		ExitCode: exitCode,
	}, nil
}
