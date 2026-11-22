package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// completionFileLimit is how many changed paths the completion check lists
// before it counts the rest.
const completionFileLimit = 20

// completionIntro is the text of the completion check, up to the list of
// changed files. It asks for the form the regression signal reads: a
// Verified block listing the commands the turn ran.
const completionIntro = `Before you end this turn, check your work and then answer in your reply to the user, in this form:

Asked: <the request in one sentence>
Verified:
- ` + "`" + `<exact command you ran in this turn>` + "`" + `: <what it showed>
Unverified: <what you changed but did not check, and why; "nothing" if all is checked>

List under Verified only commands you ran in this turn, exactly as you ran them. If you ran none, write "Verified: none". If a check is missing that you can still run, run it now before answering. If a check shows the work is unfinished or broken, fix it first.

Files changed in this turn: `

// completionGateCheck asks the model to verify its work before a turn that
// changed something ends. It is the first built-in check of the gate, and it
// is off by default until the task suite measures it.
type completionGateCheck struct{}

func (completionGateCheck) Name() string { return "completion" }

// Check asks for a verification pass when the turn changed a file or moved
// HEAD. A turn that changed nothing has nothing to check.
func (completionGateCheck) Check(_ context.Context, in gateInput) gateVerdict {
	if len(in.ChangedFiles) == 0 && in.HeadFrom == in.HeadTo {
		return gateVerdict{}
	}
	return gateVerdict{Reason: completionMessage(in.ChangedFiles, in.HeadFrom, in.HeadTo)}
}

// completionMessage renders the check's text: the requested form, the files
// the turn changed, and the commits it made.
func completionMessage(files []string, headFrom, headTo string) string {
	var b strings.Builder
	b.WriteString(completionIntro)
	listed := files
	if len(listed) > completionFileLimit {
		listed = listed[:completionFileLimit]
	}
	b.WriteString(strings.Join(listed, ", "))
	if rest := len(files) - len(listed); rest > 0 {
		fmt.Fprintf(&b, ", and %d more", rest)
	}
	if headFrom != headTo {
		// Files the model committed no longer show in the working tree,
		// so the shas are the only trace of them.
		fmt.Fprintf(&b, "\nCommits made in this turn: %s..%s", shortSHA(headFrom), shortSHA(headTo))
	}
	return b.String()
}

// shortSHA shortens a sha to the seven characters git shows.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// turnEndCompletionEnv reads the environment override once: the switch cannot
// change mid-process, and the gate asks it on every prompt.
var turnEndCompletionEnv = sync.OnceValue(readTurnEndCompletionEnv)

// readTurnEndCompletionEnv parses CRUSH_TURN_END_COMPLETION_CHECK, which
// turns the check on for one run without touching the config file.
func readTurnEndCompletionEnv() bool {
	enabled, _ := strconv.ParseBool(os.Getenv("CRUSH_TURN_END_COMPLETION_CHECK"))
	return enabled
}

// completionCheckEnabled reports whether the completion check runs. The
// config option is the normal switch; the environment variable lets a
// harness turn it on for one run.
func completionCheckEnabled(option bool) bool {
	return option || turnEndCompletionEnv()
}

// completionCheckOn reports whether this agent's config asks for the check.
func (a *sessionAgent) completionCheckOn() bool {
	option := false
	if a.cfg != nil {
		if cfg := a.cfg.Config(); cfg != nil {
			option = cfg.Options.GetTurnEndCompletionCheck()
		}
	}
	return completionCheckEnabled(option)
}
