package prompt

// CompactionStatePrefix opens the hidden user message that repeats the
// session state after a compaction. The summary describes what happened;
// this block restates the state the summary can only paraphrase, so the
// agent resumes with the live todo list, a fresh environment snapshot, the
// files the session changed and the skills it must read again.
//
// The log report recognizes the row by this tag
// (logreport.InjectionTag), which keeps it out of the hidden-turn count.
const CompactionStatePrefix = "<compaction_state>"
