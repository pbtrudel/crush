package tools

import "context"

type workingDirKey struct{}

// WithWorkingDir returns ctx carrying dir as the working directory a tool
// call resolves relative paths against and runs in. A teammate working in a
// task worktree gets its worktree this way, on every call, so its tools are
// built exactly like the Lead's and no tool description changes. An empty
// dir leaves ctx as it is.
func WithWorkingDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workingDirKey{}, dir)
}

// WorkingDirFrom returns the working directory ctx carries, or fallback,
// the directory the tool was built with.
func WorkingDirFrom(ctx context.Context, fallback string) string {
	if dir, ok := ctx.Value(workingDirKey{}).(string); ok && dir != "" {
		return dir
	}
	return fallback
}
