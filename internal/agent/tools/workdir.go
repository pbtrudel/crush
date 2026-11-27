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

type taskWorktreeKey struct{}

// WithTaskWorktree marks ctx as a call running in a task worktree, where the
// changes a bash call makes are checked against the task's write scopes after
// it returns. It travels on the call's context only, so no tool description
// or system prompt changes.
func WithTaskWorktree(ctx context.Context) context.Context {
	return context.WithValue(ctx, taskWorktreeKey{}, true)
}

// InTaskWorktree reports whether ctx is a call running in a task worktree.
func InTaskWorktree(ctx context.Context) bool {
	in, _ := ctx.Value(taskWorktreeKey{}).(bool)
	return in
}
