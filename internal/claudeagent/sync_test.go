package claudeagent

import (
	"errors"
	"slices"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func syncTestText(role fantasy.MessageRole, text string) fantasy.Message {
	return fantasy.Message{
		Role:    role,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: text}},
	}
}

func syncTestToolCall(id, name, input string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{fantasy.ToolCallPart{
			ToolCallID: id,
			ToolName:   name,
			Input:      input,
		}},
	}
}

func syncTestToolResult(id, text string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleTool,
		Content: []fantasy.MessagePart{fantasy.ToolResultPart{
			ToolCallID: id,
			Output:     fantasy.ToolResultOutputContentText{Text: text},
		}},
	}
}

func syncTestMediaMessage(mediaType string) fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: "Attached media from tool result:"},
			fantasy.FilePart{MediaType: mediaType, Data: []byte("png")},
		},
	}
}

// syncTestRecord builds a record as if the prompt had just been sent.
func syncTestRecord(prompt fantasy.Prompt, interrupted bool) *Record {
	return &Record{
		ClaudeSessionID: "claude-session",
		History:         BuildCheckpoints(prompt, nil, "", nil, "", nil),
		Interrupted:     interrupted,
	}
}

func TestGroups(t *testing.T) {
	t.Parallel()

	t.Run("skips system and invisible messages", func(t *testing.T) {
		t.Parallel()

		prompt := fantasy.Prompt{
			syncTestText(fantasy.MessageRoleSystem, "be nice"),
			{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{
				fantasy.ReasoningPart{Text: "thinking"},
			}},
			syncTestText(fantasy.MessageRoleUser, "hello"),
		}
		groups := Groups(prompt, nil)
		require.Len(t, groups, 1)
		require.Equal(t, fantasy.MessageRoleUser, groups[0].Role)
		require.Equal(t, 2, groups[0].Start)
		require.Equal(t, 3, groups[0].End)
	})

	t.Run("merges consecutive user messages", func(t *testing.T) {
		t.Parallel()

		prompt := fantasy.Prompt{
			syncTestText(fantasy.MessageRoleUser, "a"),
			syncTestText(fantasy.MessageRoleUser, "b"),
			syncTestText(fantasy.MessageRoleAssistant, "c"),
		}
		groups := Groups(prompt, nil)
		require.Len(t, groups, 2)
		require.Equal(t, fantasy.MessageRoleUser, groups[0].Role)
		require.Equal(t, 0, groups[0].Start)
		require.Equal(t, 2, groups[0].End)
		require.Equal(t, fantasy.MessageRoleAssistant, groups[1].Role)
		require.Equal(t, 2, groups[1].Start)
		require.Equal(t, 3, groups[1].End)

		members := []string{
			syncHash(syncVisible(prompt[0])),
			syncHash(syncVisible(prompt[1])),
		}
		slices.Sort(members)
		require.Equal(t, syncGroupHash(fantasy.MessageRoleUser, members), groups[0].Hash)
	})

	t.Run("stops merging at a checkpoint boundary", func(t *testing.T) {
		t.Parallel()

		prompt := fantasy.Prompt{
			syncTestText(fantasy.MessageRoleUser, "a"),
			syncTestText(fantasy.MessageRoleUser, "b"),
		}
		prefix := []Checkpoint{{
			Hash: syncGroupHash(fantasy.MessageRoleUser, []string{
				syncHash(syncVisible(prompt[0])),
			}),
		}}
		groups := Groups(prompt, prefix)
		require.Len(t, groups, 2)
		require.Equal(t, 0, groups[0].Start)
		require.Equal(t, 1, groups[0].End)
		require.Equal(t, 1, groups[1].Start)
		require.Equal(t, 2, groups[1].End)
	})

	t.Run("absorbs the attached media message", func(t *testing.T) {
		t.Parallel()

		prompt := fantasy.Prompt{
			syncTestToolCall("t1", "bash", `{"cmd":"ls"}`),
			syncTestToolResult("t1", "ok"),
			syncTestMediaMessage("image/png"),
			syncTestText(fantasy.MessageRoleUser, "next"),
		}
		groups := Groups(prompt, nil)
		require.Len(t, groups, 3)
		require.Equal(t, fantasy.MessageRoleTool, groups[1].Role)
		require.Equal(t, 1, groups[1].Start)
		require.Equal(t, 3, groups[1].End)
		require.Equal(t, fantasy.MessageRoleUser, groups[2].Role)
		require.Equal(t, 3, groups[2].Start)
	})

	t.Run("coalesces text around reasoning", func(t *testing.T) {
		t.Parallel()

		prompt := fantasy.Prompt{
			{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
				fantasy.TextPart{Text: "a"},
				fantasy.ReasoningPart{Text: "why"},
				fantasy.TextPart{Text: "b"},
			}},
		}
		groups := Groups(prompt, nil)
		require.Len(t, groups, 1)
		require.Equal(t, syncHash(syncVisible(syncTestText(fantasy.MessageRoleAssistant, "ab"))), syncHash(syncVisible(prompt[0])))
	})

	t.Run("hashes canonically", func(t *testing.T) {
		t.Parallel()

		// Locked value: sha256 of
		// {"members":["<sha256 of [{"text":"hello","type":"text"}]>"],"role":"user"}.
		groups := Groups(fantasy.Prompt{syncTestText(fantasy.MessageRoleUser, "hello")}, nil)
		require.Len(t, groups, 1)
		require.Equal(t, "0e6af4e74ce7fde9e794d6e4b017763c74ecbd924aa13a3adb4bcdf95b7a97f7", groups[0].Hash)

		// Locked value: the same form for a tool call whose input is parsed so
		// its keys sort and its number keeps its literal.
		toolPrompt := fantasy.Prompt{syncTestToolCall("t1", "bash", `{"n":2,"cmd":"ls"}`)}
		toolGroups := Groups(toolPrompt, nil)
		require.Len(t, toolGroups, 1)
		require.Equal(t, "20cf1900c0765ccb3b3aac9d2e4cf96757c53d39b99d73a440c3fd9c65d73b06", toolGroups[0].Hash)
	})
}

func TestDecide(t *testing.T) {
	t.Parallel()

	base := fantasy.Prompt{
		syncTestText(fantasy.MessageRoleUser, "hi"),
		syncTestText(fantasy.MessageRoleAssistant, "hello"),
	}

	tests := []struct {
		name   string
		prompt fantasy.Prompt
		record *Record
		want   Decision
	}{
		{
			name:   "fresh without a record",
			prompt: base,
			want:   Decision{Kind: DecisionFresh},
		},
		{
			name:   "fresh when nothing matches",
			prompt: fantasy.Prompt{syncTestText(fantasy.MessageRoleUser, "unrelated")},
			record: syncTestRecord(base, false),
			want:   Decision{Kind: DecisionFresh},
		},
		{
			name:   "continue when only user messages are new",
			prompt: append(slices.Clone(base), syncTestText(fantasy.MessageRoleUser, "again")),
			record: syncTestRecord(base, false),
			want:   Decision{Kind: DecisionContinue, Start: 2},
		},
		{
			name: "catch-up when a turn is missing",
			prompt: append(slices.Clone(base),
				syncTestToolCall("t1", "bash", `{"cmd":"ls"}`),
				syncTestToolResult("t1", "ok"),
				syncTestText(fantasy.MessageRoleUser, "next"),
			),
			record: syncTestRecord(base, false),
			want:   Decision{Kind: DecisionCatchUp, Start: 2},
		},
		{
			name: "changed when a saved group no longer matches",
			prompt: fantasy.Prompt{
				syncTestText(fantasy.MessageRoleUser, "hi"),
				syncTestText(fantasy.MessageRoleAssistant, "different"),
			},
			record: syncTestRecord(base, false),
			want:   Decision{Kind: DecisionChanged},
		},
		{
			name: "interrupted skips the partial reply",
			prompt: append(slices.Clone(base),
				syncTestText(fantasy.MessageRoleAssistant, "partial"),
				syncTestText(fantasy.MessageRoleUser, "next"),
			),
			record: syncTestRecord(base, true),
			want:   Decision{Kind: DecisionContinue, Start: 3},
		},
		{
			name: "interrupted with nothing new is changed",
			prompt: append(slices.Clone(base),
				syncTestText(fantasy.MessageRoleAssistant, "partial"),
			),
			record: syncTestRecord(base, true),
			want:   Decision{Kind: DecisionChanged},
		},
		{
			name: "interrupted keeps assistant groups with tool calls",
			prompt: append(slices.Clone(base),
				syncTestToolCall("t1", "bash", `{"cmd":"ls"}`),
			),
			record: syncTestRecord(base, true),
			want:   Decision{Kind: DecisionCatchUp, Start: 2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, Decide(test.prompt, test.record))
		})
	}
}

func TestCatchUpContent(t *testing.T) {
	t.Parallel()

	prompt := fantasy.Prompt{
		syncTestText(fantasy.MessageRoleUser, "old"),
		syncTestText(fantasy.MessageRoleUser, "hi"),
		syncTestText(fantasy.MessageRoleAssistant, "hello"),
		syncTestText(fantasy.MessageRoleUser, "new"),
	}

	t.Run("wraps history before the last turn", func(t *testing.T) {
		t.Parallel()

		blocks := CatchUpContent(prompt, 1)
		require.Equal(t, []ContentBlock{
			{Type: "text", Text: syncCatchUpOpen},
			{Type: "text", Text: "<user>"},
			{Type: "text", Text: "hi"},
			{Type: "text", Text: "</user>"},
			{Type: "text", Text: "<assistant>"},
			{Type: "text", Text: "hello"},
			{Type: "text", Text: "</assistant>"},
			{Type: "text", Text: syncCatchUpClose},
			{Type: "text", Text: "new"},
		}, blocks)
	})

	t.Run("does not wrap when every turn is a user message", func(t *testing.T) {
		t.Parallel()

		blocks := CatchUpContent(fantasy.Prompt{
			syncTestText(fantasy.MessageRoleUser, "hi"),
			syncTestText(fantasy.MessageRoleUser, "there"),
		}, 0)
		require.Equal(t, []ContentBlock{
			{Type: "text", Text: "hi"},
			{Type: "text", Text: "there"},
		}, blocks)
	})

	t.Run("clamps a negative start", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, CatchUpContent(prompt, 0), CatchUpContent(prompt, -5))
	})

	t.Run("empty when start is past the end", func(t *testing.T) {
		t.Parallel()

		require.Empty(t, CatchUpContent(prompt, len(prompt)))
		require.Empty(t, CatchUpContent(prompt, len(prompt)+3))
	})
}

func TestCheckpoints(t *testing.T) {
	t.Parallel()

	prompt := fantasy.Prompt{
		syncTestText(fantasy.MessageRoleUser, "hi"),
		syncTestText(fantasy.MessageRoleAssistant, "hello"),
	}
	groups := Groups(prompt, nil)
	require.Len(t, groups, 2)

	t.Run("records one checkpoint per group", func(t *testing.T) {
		t.Parallel()

		history := BuildCheckpoints(prompt, nil, "", nil, "", nil)
		require.Equal(t, []Checkpoint{
			{Hash: groups[0].Hash},
			{Hash: groups[1].Hash},
		}, history)
	})

	t.Run("carries over unchanged message ids", func(t *testing.T) {
		t.Parallel()

		previous := &Record{History: []Checkpoint{
			{Hash: groups[0].Hash, ClaudeMessageID: "m0"},
			{Hash: groups[1].Hash, ClaudeMessageID: "m1"},
		}}
		history := BuildCheckpoints(prompt, previous, "", nil, "", nil)
		require.Equal(t, []Checkpoint{
			{Hash: groups[0].Hash, ClaudeMessageID: "m0"},
			{Hash: groups[1].Hash, ClaudeMessageID: "m1"},
		}, history)
	})

	t.Run("does not carry over a changed group", func(t *testing.T) {
		t.Parallel()

		previous := &Record{History: []Checkpoint{
			{Hash: groups[0].Hash, ClaudeMessageID: "m0"},
			{Hash: "other", ClaudeMessageID: "m1"},
		}}
		history := BuildCheckpoints(prompt, previous, "", nil, "", nil)
		require.Equal(t, []Checkpoint{
			{Hash: groups[0].Hash, ClaudeMessageID: "m0"},
			{Hash: groups[1].Hash},
		}, history)
	})

	t.Run("uses the tool message uuid", func(t *testing.T) {
		t.Parallel()

		toolPrompt := fantasy.Prompt{
			syncTestToolCall("t1", "bash", `{"cmd":"ls"}`),
			syncTestToolResult("t1", "ok"),
		}
		history := BuildCheckpoints(toolPrompt, nil, "", nil, "", map[string]string{"t1": "uuid-1"})
		require.Len(t, history, 2)
		require.Empty(t, history[0].ClaudeMessageID)
		require.Equal(t, "uuid-1", history[1].ClaudeMessageID)
	})

	t.Run("gives the last group the user id", func(t *testing.T) {
		t.Parallel()

		history := BuildCheckpoints(prompt, nil, "user-uuid", nil, "", nil)
		require.Len(t, history, 2)
		require.Empty(t, history[0].ClaudeMessageID)
		require.Equal(t, "user-uuid", history[1].ClaudeMessageID)
	})

	t.Run("appends the reply", func(t *testing.T) {
		t.Parallel()

		reply := []ReplyPart{
			{Text: "done"},
			{ToolCallID: "t1", ToolName: "bash", Input: `{"cmd":"ls"}`},
		}
		history := BuildCheckpoints(prompt, nil, "", reply, "reply-uuid", nil)
		require.Len(t, history, 3)
		require.Equal(t, "reply-uuid", history[2].ClaudeMessageID)

		replyGroups := Groups(fantasy.Prompt{{
			Role:    fantasy.MessageRoleAssistant,
			Content: syncReplyContent(reply),
		}}, nil)
		require.Len(t, replyGroups, 1)
		require.Equal(t, replyGroups[0].Hash, history[2].Hash)
	})
}

func TestConvertPrompt(t *testing.T) {
	t.Parallel()

	t.Run("text only has no role tags", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, []ContentBlock{
			{Type: "text", Text: "hi"},
		}, ConvertPrompt(fantasy.Prompt{syncTestText(fantasy.MessageRoleUser, "hi")}))
	})

	t.Run("wraps roles once history is present", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{
			syncTestText(fantasy.MessageRoleUser, "hi"),
			syncTestText(fantasy.MessageRoleAssistant, "hello"),
		})
		require.Equal(t, []ContentBlock{
			{Type: "text", Text: "<user>"},
			{Type: "text", Text: "hi"},
			{Type: "text", Text: "</user>"},
			{Type: "text", Text: "<assistant>"},
			{Type: "text", Text: "hello"},
			{Type: "text", Text: "</assistant>"},
		}, blocks)
	})

	t.Run("skips system messages", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{
			syncTestText(fantasy.MessageRoleSystem, "be nice"),
			syncTestText(fantasy.MessageRoleUser, "hi"),
		})
		require.Equal(t, []ContentBlock{{Type: "text", Text: "hi"}}, blocks)
	})

	t.Run("converts images", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{{
			Role: fantasy.MessageRoleUser,
			Content: []fantasy.MessagePart{
				fantasy.FilePart{MediaType: "image/png", Data: []byte("png")},
			},
		}})
		require.Equal(t, []ContentBlock{{
			Type: "image",
			Source: &ContentSource{
				Type:      "base64",
				MediaType: "image/png",
				Data:      "cG5n",
			},
		}}, blocks)
	})

	t.Run("converts documents", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{{
			Role: fantasy.MessageRoleUser,
			Content: []fantasy.MessagePart{
				fantasy.FilePart{MediaType: "application/pdf", Data: []byte("pdf")},
			},
		}})
		require.Equal(t, []ContentBlock{{
			Type: "document",
			Source: &ContentSource{
				Type:      "base64",
				MediaType: "application/pdf",
				Data:      "cGRm",
			},
		}}, blocks)
	})

	t.Run("drops unsupported attachments", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{{
			Role: fantasy.MessageRoleUser,
			Content: []fantasy.MessagePart{
				fantasy.FilePart{MediaType: "audio/mp3", Data: []byte("mp3")},
			},
		}})
		require.Empty(t, blocks)
	})

	t.Run("converts tool calls", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{syncTestToolCall("t1", "bash", `{"cmd":"ls"}`)})
		require.Equal(t, []ContentBlock{
			{Type: "text", Text: "<assistant>"},
			{Type: "text", Text: "<tool-call>\n{\"id\":\"t1\",\"name\":\"bash\",\"input\":{\"cmd\":\"ls\"}}\n</tool-call>"},
			{Type: "text", Text: "</assistant>"},
		}, blocks)
	})

	t.Run("converts tool results", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{
			syncTestToolCall("t1", "bash", `{"cmd":"ls"}`),
			syncTestToolResult("t1", "ok"),
		})
		require.Equal(t, []ContentBlock{
			{Type: "text", Text: "<assistant>"},
			{Type: "text", Text: "<tool-call>\n{\"id\":\"t1\",\"name\":\"bash\",\"input\":{\"cmd\":\"ls\"}}\n</tool-call>"},
			{Type: "text", Text: "</assistant>"},
			{Type: "text", Text: "<tool>"},
			{Type: "text", Text: "<tool-result>\n{\"id\":\"t1\",\"name\":\"bash\",\"output\":{\"type\":\"text\",\"value\":\"ok\"}}\n</tool-result>"},
			{Type: "text", Text: "</tool>"},
		}, blocks)
	})

	t.Run("converts error results", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "t1",
					Output:     fantasy.ToolResultOutputContentError{Error: errors.New("boom")},
				},
			},
		}})
		require.Equal(t, []ContentBlock{
			{Type: "text", Text: "<tool>"},
			{Type: "text", Text: "<tool-result>\n{\"id\":\"t1\",\"name\":\"\",\"output\":{\"type\":\"error\",\"value\":\"boom\"}}\n</tool-result>"},
			{Type: "text", Text: "</tool>"},
		}, blocks)
	})

	t.Run("converts media results", func(t *testing.T) {
		t.Parallel()

		blocks := ConvertPrompt(fantasy.Prompt{{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "t1",
					Output: fantasy.ToolResultOutputContentMedia{
						Data:      "cG5n",
						MediaType: "image/png",
						Text:      "shot",
					},
				},
			},
		}})
		require.Equal(t, []ContentBlock{
			{Type: "text", Text: "<tool>"},
			{Type: "text", Text: "<tool-result>\n{\"id\":\"t1\",\"name\":\"\",\"output\":{\"data\":\"cG5n\",\"mediaType\":\"image/png\",\"type\":\"media\"}}\n</tool-result>"},
			{Type: "text", Text: "</tool>"},
		}, blocks)
	})
}
